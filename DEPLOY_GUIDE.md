# WebSocket修复部署指南

## 当前状态

✅ **修复已完成**：
- WebSocket自动重连机制已增强
- 断流检测优化（2秒检测，5秒防抖）
- 压缩验证已添加
- 消息限流已实施（价格变化<0.01%过滤）

✅ **压缩已生效**：
从日志可见：`WebSocket压缩协商: permessage-deflate`

## 部署步骤

### 方法1：使用部署脚本（推荐）

```bash
cd /root/market-maker-go
./scripts/deploy_websocket_fix.sh
```

脚本会自动：
1. 停止旧进程
2. 验证配置和可执行文件
3. 启动新进程
4. 显示关键日志

### 方法2：手动部署

```bash
cd /root/market-maker-go

# 1. 停止旧进程
PID=$(cat logs/phoenix_live.pid 2>/dev/null || ps aux | grep "bin/phoenix" | grep -v grep | awk '{print $2}')
if [ -n "$PID" ]; then
    kill -TERM "$PID"
    sleep 3
    kill -9 "$PID" 2>/dev/null || true
fi

# 2. 重新编译（如果代码有更新）
make build

# 3. 启动新进程
nohup ./bin/phoenix --config configs/phoenix_live.yaml > logs/phoenix_live.out 2>&1 &
echo $! > logs/phoenix_live.pid

# 4. 查看日志
tail -f logs/phoenix_live.out | grep -E "(WebSocket|重连|压缩|ERR|告警)"
```

## 验证修复效果

### 1. 检查WebSocket连接

```bash
tail -f logs/phoenix_live.out | grep -E "(WebSocket连接成功|WebSocket压缩协商)"
```

**预期输出**：
```
WebSocket连接成功，开始读取消息...
WebSocket压缩协商: permessage-deflate; server_no_context_takeover; client_no_context_takeover
```

### 2. 检查断流检测

```bash
tail -f logs/phoenix_live.out | grep -E "(价格数据过期|WebSocket可能断流|重连)"
```

**预期行为**：
- 如果出现断流，应该在2秒内检测到
- 应该在5秒内自动重连
- 应该看到"WebSocket自动重连成功"日志

### 3. 检查流量

```bash
# 启动流量监控
./scripts/monitor_traffic.sh

# 或查看Prometheus指标
curl http://localhost:9090/metrics | grep phoenix_ws_bandwidth
```

**预期结果**：
- 如果压缩生效，流量应该降低30-40%
- 如果仍>400k，考虑切换到3交易对配置

## 如果流量仍高

### 切换到3交易对配置

```bash
# 停止当前进程
PID=$(cat logs/phoenix_live.pid 2>/dev/null || ps aux | grep "bin/phoenix" | grep -v grep | awk '{print $2}')
kill -TERM "$PID" 2>/dev/null || true
sleep 3

# 使用3交易对配置启动
nohup ./bin/phoenix --config configs/phoenix_live_3symbols.yaml > logs/phoenix_live.out 2>&1 &
echo $! > logs/phoenix_live.pid
```

**预期效果**：
- 流量立即降低40%（从5个交易对降到3个）
- 保留高流动性交易对（ETH/DOGE/SOL）

## 监控命令

```bash
# 实时日志
tail -f logs/phoenix_live.out

# WebSocket相关
tail -f logs/phoenix_live.out | grep -E "(WebSocket|重连|压缩|断流)"

# 错误监控
tail -f logs/phoenix_live.out | grep ERR

# 流量监控
./scripts/monitor_traffic.sh

# 进程状态
ps aux | grep phoenix | grep -v grep
```

## 故障排查

### 问题1：进程启动失败

**检查**：
```bash
tail -50 logs/phoenix_live.out
```

**可能原因**：
- 配置文件错误
- 端口被占用
- 权限问题

### 问题2：WebSocket连接失败

**检查**：
```bash
tail -f logs/phoenix_live.out | grep -E "(ws dial failed|WebSocket运行错误)"
```

**可能原因**：
- 网络问题
- Binance API限制
- 防火墙阻止

### 问题3：流量仍高

**解决方案**：
1. 确认压缩是否生效（查看日志中的"WebSocket压缩协商"）
2. 如果压缩无效，切换到3交易对配置
3. 考虑进一步优化（如降低depth更新频率）

## 回滚方案

如果需要回滚到修复前的版本：

```bash
# 1. 停止当前进程
PID=$(cat logs/phoenix_live.pid 2>/dev/null || ps aux | grep "bin/phoenix" | grep -v grep | awk '{print $2}')
kill -TERM "$PID" 2>/dev/null || true

# 2. 使用git回滚代码（如果需要）
# git checkout <previous-commit>

# 3. 重新编译
make build

# 4. 启动
nohup ./bin/phoenix --config configs/phoenix_live.yaml > logs/phoenix_live.out 2>&1 &
echo $! > logs/phoenix_live.pid
```

## 总结

✅ **修复已完成**，代码已编译通过
✅ **压缩已生效**（从日志可见）
⏳ **需要重启**以应用所有修复（断流检测优化、消息限流等）

**下一步**：
1. 运行部署脚本：`./scripts/deploy_websocket_fix.sh`
2. 观察日志，确认修复生效
3. 监控流量，如果仍>400k，切换到3交易对配置


