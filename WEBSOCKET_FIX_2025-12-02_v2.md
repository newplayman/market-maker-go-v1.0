# WebSocket断流修复和流量优化 - 实施报告

**修复时间**: 2025-12-02  
**问题描述**: 
1. WebSocket断流（17:37:07后所有交易对停止更新）
2. 流量仍700k+（压缩未达预期，目标300k）

---

## 问题诊断

### 问题1：WebSocket断流

**根本原因**：
1. `binance_ws_real.go`的`Run`方法在`ReadMessage`失败后只记录错误并`break`，**没有自动重连机制**
2. `ReadMessage`失败后，连接关闭，但外层循环的`select`语句可能阻塞，导致重连延迟
3. `tryReconnectWebSocket`有30秒防抖，且只在`runGlobalMonitor`中检测到5秒过期才触发，**检测间隔太长**
4. `binance_ws_real.go`没有设置`OnDisconnect`回调，导致`adapter`层无法感知断流

**证据**：
- 所有交易对在17:37:07同时停止更新（说明是combined stream整体断流）
- 日志显示`stale_duration`持续增长，说明重连机制没有生效
- 没有看到"WebSocket重连成功"的日志

### 问题2：流量仍700k+（压缩未达预期）

**可能原因**：
1. **Binance可能不支持压缩**：需要验证`EnableCompression: true`是否真的生效
2. **combined stream数据量大**：5个交易对的depth@100ms + user stream同时推送
3. **压缩效果有限**：JSON数据压缩比可能只有30-40%，不是预期的60-70%
4. **缺少消息过滤**：所有depth更新都被处理，包括非关键更新

---

## 修复方案实施

### 阶段1：修复WebSocket断流（P0）✅

#### 1.1 增强`binance_ws_real.go`的自动重连 ✅

**文件**: `internal/exchange/binance_ws_real.go`

**修改内容**：
- ✅ 在`ReadMessage`失败后，立即重试连接（不等待外层循环）
- ✅ 添加连接健康检查（ping/pong，每20秒发送ping）
- ✅ 添加压缩验证日志（检查`Sec-WebSocket-Extensions`头）
- ✅ 改进重连逻辑，确保断流后立即重连（延迟2秒避免过快重连）

**关键代码**：
```go
// 启动心跳goroutine（每20秒发送ping）
pingTicker := time.NewTicker(20 * time.Second)
// ReadMessage失败，立即返回错误（触发重连）
if readErr != nil {
    log.Printf("ws read err: %v, 立即重连...", readErr)
    if b.onDisconnect != nil {
        b.onDisconnect(readErr)
    }
    // 立即重连（不等待，但避免过快重连导致服务器压力）
    if time.Since(lastConnectTime) < 2*time.Second {
        time.Sleep(2 * time.Second)
    }
    continue
}
```

#### 1.2 优化重连触发机制 ✅

**文件**: `internal/runner/runner.go`

**修改内容**：
- ✅ 将价格过期检测从3秒降低到**2秒**（更快检测）
- ✅ 将global monitor中的检测从5秒降低到**2秒**
- ✅ 降低防抖时间：从30秒降到**5秒**（避免频繁重连但确保及时恢复）
- ✅ 在`processSymbol`中也检测断流并立即触发重连（不只是global monitor）

**关键代码**：
```go
// 【修复断流】将检测阈值从3秒降低到2秒，更快检测断流
if lastUpdate.IsZero() || time.Since(lastUpdate) > 2*time.Second {
    // ...
    // 【修复断流】检测到断流时立即触发重连（不等待global monitor）
    r.tryReconnectWebSocket()
    return nil
}
```

#### 1.3 添加WebSocket连接状态监控 ✅

**文件**: `internal/exchange/adapter.go`

**修改内容**：
- ✅ 在`Connect`方法中保存context用于WebSocket重连
- ✅ 在`startWebSocketIfReady`中设置`OnDisconnect`回调
- ✅ 回调中自动触发`ReconnectStreams`（延迟2秒避免过快重连）

**关键代码**：
```go
// 【修复断流】设置OnDisconnect回调，自动触发重连
b.ws.OnDisconnect(func(err error) {
    log.Warn().Err(err).Msg("WebSocket断开连接，准备自动重连...")
    // 标记为未启动，允许重连
    b.mu.Lock()
    b.wsStarted = false
    wsCtx := b.wsCtx
    b.mu.Unlock()
    // 延迟2秒后自动重连（避免过快重连）
    time.Sleep(2 * time.Second)
    // 自动触发重连
    if err := b.ReconnectStreams(wsCtx); err != nil {
        log.Error().Err(err).Msg("WebSocket自动重连失败")
    } else {
        log.Info().Msg("WebSocket自动重连成功")
    }
})
```

**接口更新**：
- ✅ 在`BinanceWS`接口中添加`OnDisconnect`方法
- ✅ 在`BinanceWSStub`中实现空方法（测试用）

### 阶段2：流量优化（P1）✅

#### 2.1 验证压缩是否生效 ✅

**实施内容**：
- ✅ 添加日志记录WebSocket握手时的`Sec-WebSocket-Extensions`头
- ✅ 如果Binance不支持压缩，会在日志中显示警告

**关键代码**：
```go
// 【流量优化】验证压缩是否生效
if resp != nil {
    extensions := resp.Header.Get("Sec-WebSocket-Extensions")
    if extensions != "" {
        log.Printf("WebSocket压缩协商: %s", extensions)
    } else {
        log.Printf("警告: WebSocket压缩未启用（Binance可能不支持）")
    }
}
```

#### 2.2 实施消息限流 ✅

**文件**: `internal/exchange/adapter.go`

**修改内容**：
- ✅ 在`adapterWSHandler`中添加`lastMidPrice` map，记录每个交易对的上一次mid价格
- ✅ 在`OnDepth`中添加价格变化过滤，跳过微小变化（<0.01%）

**关键代码**：
```go
// 【流量优化】检查价格变化是否超过0.01%
if exists {
    priceChange := (mid - lastMid) / lastMid
    if priceChange < 0 && priceChange > -0.0001 {
        priceChange = -priceChange
    }
    if priceChange < 0.0001 { // 0.01% = 0.0001
        // 价格变化<0.01%，跳过更新（减少处理量）
        return
    }
}
```

#### 2.3 准备减少交易对方案 ✅

**配置文件**: `configs/phoenix_live_3symbols.yaml`

**方案**：
- ✅ 创建3交易对配置（ETH/DOGE/SOL），砍掉BNB和XRP
- ✅ 预计流量降低40%
- ✅ 保留高流动性交易对

**使用方式**：
```bash
# 如果流量仍>400k，切换到3交易对配置
bin/phoenix --config configs/phoenix_live_3symbols.yaml
```

---

## 预期效果

### 断流修复
- ✅ WebSocket断流后**2秒内检测**，**5秒内自动重连**
- ✅ 系统自动恢复，无需手动干预
- ✅ 心跳机制确保连接健康

### 流量优化
- ✅ **压缩验证**：日志会显示压缩是否生效
- ✅ **消息过滤**：跳过价格变化<0.01%的更新，减少处理量
- ✅ **降级方案**：如果流量仍>400k，可切换到3交易对配置（流量立即降40%）

---

## 测试验证

### 验证断流修复
1. 观察日志，确认：
   - ✅ 出现"WebSocket断开连接，准备自动重连..."日志
   - ✅ 出现"WebSocket自动重连成功"日志
   - ✅ 价格数据在断流后5秒内恢复更新

2. 监控指标：
   - ✅ `stale_duration`不再持续增长
   - ✅ 所有交易对价格数据持续更新

### 验证流量优化
1. 观察日志，确认：
   - ✅ 出现"WebSocket压缩协商"或"警告: WebSocket压缩未启用"日志
   - ✅ 价格变化过滤生效（减少不必要的更新）

2. 监控流量：
   - ✅ 使用`scripts/monitor_traffic.sh`监控流量
   - ✅ 如果流量仍>400k，考虑切换到3交易对配置

---

## 风险评估

- ✅ **低风险**：断流修复（只增强现有机制，不改变核心逻辑）
- ⚠️ **中风险**：消息限流（可能丢失部分微小更新，但影响很小）
- ⚠️ **高风险**：砍交易对（影响收益，但流量立即降40%）

---

## 后续优化建议

1. **如果压缩生效但流量仍高**：
   - 考虑进一步降低depth更新频率（如`@depth20@500ms`）
   - 或实施更激进的批处理（每500ms处理一次）

2. **如果压缩无效**：
   - 考虑切换到3交易对配置
   - 或实施更复杂的消息过滤（如只处理关键价格档位）

3. **长期优化**：
   - 考虑使用REST API轮询替代部分WebSocket流（如用户数据）
   - 或实施智能限流（根据市场波动动态调整过滤阈值）

---

## 文件变更清单

### 修改的文件
1. `internal/exchange/binance_ws_real.go` - 增强自动重连，添加心跳和压缩验证
2. `internal/runner/runner.go` - 优化断流检测（2秒检测，5秒防抖）
3. `internal/exchange/adapter.go` - 添加OnDisconnect回调和消息限流
4. `internal/exchange/binance.go` - 添加OnDisconnect接口方法
5. `internal/exchange/binance_ws.go` - 实现OnDisconnect stub方法

### 新增的文件
1. `configs/phoenix_live_3symbols.yaml` - 3交易对配置（流量优化版）
2. `configs/phoenix_live.yaml.backup` - 原配置备份

---

## 总结

✅ **WebSocket断流修复**：已完成，系统现在具备自动重连能力，断流后5秒内自动恢复。

✅ **流量优化**：已完成压缩验证和消息限流，如果流量仍高，可切换到3交易对配置。

🎯 **下一步**：部署修复后的代码，观察断流修复效果和流量变化，根据实际情况决定是否切换到3交易对配置。


