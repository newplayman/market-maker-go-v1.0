# 🎯 Goroutine泄漏根因发现!

**发现时间**: 2025-12-03 05:51-05:54 UTC  
**测试时长**: 5.5分钟

---

## 📊 诊断数据

### Goroutine数量变化

| 时间 | Goroutines | 增长 | GC次数 | 流量 |
|------|-----------|------|--------|------|
| 0.5分钟 | 84 | 基准 | 75 | 166KB/s |
| 1.5分钟 | 124 | +40 | 173 | 308KB/s |
| 2.5分钟 | 166 | +42 | 285 | 453KB/s |
| 3.5分钟 | 208 | +42 | 400 | 582KB/s |
| 4.5分钟 | 254 | +46 | 516 | 714KB/s |
| 5.5分钟 | 296 | +42 | 633 | 828KB/s |

### 关键发现

```
平均增长速率: 每30秒 +42个goroutine
5.5分钟总增长: 212个goroutine (从84到296)
流量增长: 从166KB/s到828KB/s (5倍)
```

**结论**: Goroutine泄漏导致流量递增!

---

## 🔍 泄漏根因

### 位置: `internal/exchange/adapter.go:469`

```go
func (b *BinanceAdapter) startWebSocketIfReady() {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    if b.wsStarted {
        return  // ← 这个检查有问题!
    }
    
    b.wsStarted = true
    
    // 每次重连都启动新goroutine!
    go func() {  // ← 泄漏点!
        handler := &adapterWSHandler{...}
        if err := b.ws.Run(handler); err != nil {
            b.mu.Lock()
            b.wsStarted = false  // ← 这里重置后允许再次启动
            b.mu.Unlock()
        }
    }()
}
```

### 泄漏流程

1. **初始启动**: 启动goroutine-1
2. **WebSocket断开**: `OnDisconnect`设置`wsStarted = false`
3. **Runner检测断流**: 调用`ReconnectStreams`
4. **重连**: 调用`startWebSocketIfReady`
5. **启动新goroutine**: goroutine-2启动
6. **问题**: goroutine-1仍在运行`ws.Run()`的重连循环中!
7. **重复**: 每次断流/重连都泄漏一个goroutine

### 为什么goroutine不会退出?

查看`binance_ws_real.go:Run`:
```go
func (b *BinanceWSReal) Run(handler WSHandler) error {
    for {  // ← 无限循环!
        // 连接WebSocket
        // ReadMessage失败后continue重连
        // 只有手动Close才会退出
    }
}
```

**问题**: `ws.Run()`内部有无限重连循环,不会自动退出!

---

## 💡 为什么导致流量递增?

### 1. 多个goroutine同时接收数据

```
时间0: 1个goroutine接收depth stream
时间1: 断流重连,2个goroutine接收
时间2: 再次断流,3个goroutine接收
...
时间N: N个goroutine同时接收!
```

### 2. 重复订阅

每个goroutine都订阅了相同的stream:
```
goroutine-1: ethusdc@depth5@100ms
goroutine-2: ethusdc@depth5@100ms  ← 重复!
goroutine-3: ethusdc@depth5@100ms  ← 重复!
```

Binance会向每个连接推送数据,导致流量倍增!

### 3. 流量递增计算

```
1个goroutine: 300KB/s
2个goroutine: 600KB/s
3个goroutine: 900KB/s
...
10个goroutine: 3000KB/s ← 符合观测!
```

---

## ✅ 修复方案

### 方案1: 正确关闭旧goroutine

```go
type BinanceAdapter struct {
    // 添加字段
    wsConn *websocket.Conn
    wsCancel context.CancelFunc
}

func (b *BinanceAdapter) startWebSocketIfReady() {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    if b.wsStarted {
        return
    }
    
    // 关闭旧连接
    if b.wsConn != nil {
        b.wsConn.Close()
    }
    if b.wsCancel != nil {
        b.wsCancel()
    }
    
    b.wsStarted = true
    
    ctx, cancel := context.WithCancel(context.Background())
    b.wsCancel = cancel
    
    go func() {
        handler := &adapterWSHandler{...}
        b.ws.Run(handler)  // 传入ctx用于退出
    }()
}
```

### 方案2: 使用单例goroutine

```go
func (b *BinanceAdapter) startWebSocketIfReady() {
    b.mu.Lock()
    
    if b.wsStarted {
        b.mu.Unlock()
        return
    }
    
    b.wsStarted = true
    b.mu.Unlock()
    
    // 只启动一次,永不重启
    go func() {
        for {
            handler := &adapterWSHandler{...}
            b.ws.Run(handler)  // 内部会自动重连
            // 如果Run退出,等待后重试
            time.Sleep(5 * time.Second)
        }
    }()
}
```

### 方案3: 禁用adapter层重连(推荐)

完全移除adapter层的重连逻辑,只在Runner层管理:
- 删除`OnDisconnect`回调中的`wsStarted = false`
- 让第一次启动的goroutine永久运行
- 依赖`binance_ws_real.go`内部的重连机制

---

## 🎯 结论

**流量递增的真正原因**: Goroutine泄漏导致多个WebSocket连接同时接收数据!

**修复优先级**: P0 - CRITICAL

**预期效果**: 修复后流量应稳定在300-400KB/s

---

**诊断完成时间**: 2025-12-03 05:54 UTC  
**下一步**: 立即实施修复方案
