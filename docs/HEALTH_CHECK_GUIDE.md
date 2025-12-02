# Phoenix做市商健康检查与故障恢复指南

## 概述

本指南介绍如何使用健康检查工具来监控Phoenix做市商进程，及时发现"假死"状态并采取措施。

## 问题背景

### 什么是"假死"？

"假死"是指进程仍在运行，但实际上已经停止正常工作，表现为：
- 进程存在但订单不更新
- WebSocket连接断开但未检测到
- 价格数据过期但继续使用
- 在极端行情下造成重大损失

### 根本原因

经诊断，假死的主要原因包括：
1. **WebSocket静默断流**：连接存在但数据不推送
2. **listenKey过期**：用户数据流断开，无法收到成交回报
3. **Goroutine Panic**：某个关键goroutine崩溃
4. **防闪烁容差过大**：价格偏离但不更新订单

## 已实施的修复

### 1. 价格新鲜度检测（P0）

**修改文件**: `internal/runner/runner.go:213-230`

**改进**:
- 将价格过期阈值从10秒降低到**3秒**
- 将日志级别从WARN提升到**ERROR**
- 添加stale_duration记录，便于分析

**效果**: 更快检测WebSocket断流，避免使用过期价格

### 2. WebSocket健康监控（P0）

**修改文件**: `internal/runner/runner.go:745-775`

**改进**:
- 在全局监控中每秒检查深度数据更新时间
- 超过5秒未更新时输出严重告警
- 记录到metrics供Prometheus监控

**效果**: 主动检测WebSocket异常，而不是被动等待

### 3. Panic恢复机制（P0）

**修改文件**: `internal/runner/runner.go:126-152, 724-746`

**改进**:
- 为`runSymbol()`和`runGlobalMonitor()`添加defer panic恢复
- Panic发生时记录详细信息和堆栈
- 自动等待5秒后重启崩溃的goroutine

**效果**: 单个goroutine崩溃不会导致整个系统假死

### 4. 健康检查脚本（P0）

**新增文件**: `scripts/health_check.sh`

**功能**:
1. ✓ 进程状态检查
2. ✓ 日志活跃度检查
3. ✓ 价格数据新鲜度检查
4. ✓ 订单活跃度检查
5. ✓ 错误和告警检查
6. ✓ 活跃订单数量检查

**输出**: 
- 退出码0=健康
- 退出码1=警告
- 退出码2=严重异常

## 使用指南

### 手动健康检查

```bash
cd /root/market-maker-go
./scripts/health_check.sh
```

**输出示例**（健康）:
```
================================================
Phoenix 做市商健康检查
时间: 2025-12-02 13:00:00
================================================

[检查1] 进程状态
✓ 进程运行中 (PID: 12345)

[检查2] 日志活跃度
✓ 日志正常更新 (2秒前)

[检查3] 价格数据新鲜度
✓ 价格数据正常 (最近有50次报价生成)

[检查4] 订单活跃度
✓ 订单操作正常 (最近30次操作)

[检查5] 错误和告警
✓ 无错误日志

[检查6] 活跃订单检查
✓ 活跃订单数量正常 (14个)

================================================
整体状态: 健康 ✓
```

**输出示例**（异常）:
```
================================================
整体状态: 严重异常 ✗
建议: 立即检查日志，考虑重启进程

紧急处理命令:
  查看日志: tail -100 logs/phoenix_live.out
  重启进程: ./scripts/stop_live.sh && ./scripts/start_live.sh
  紧急平仓: ./bin/emergency cancel-all
```

### 自动健康检查（推荐）

使用后台守护进程持续监控：

```bash
# 启动自动健康检查（30秒间隔）
nohup ./scripts/auto_health_check.sh > logs/auto_health.log 2>&1 &

# 查看监控日志
tail -f logs/auto_health.log

# 查看健康检查详细日志
tail -f logs/health_check.log
```

**配置选项**（编辑 `auto_health_check.sh`）:
```bash
CHECK_INTERVAL=30          # 检查间隔（秒），建议30-60秒
MAX_FAILURES=3             # 连续失败次数阈值，超过后告警
AUTO_RESTART=false         # 是否自动重启（谨慎开启！）
```

### 定时任务（Crontab）

如果不使用守护进程，可以用cron定期检查：

```bash
# 编辑crontab
crontab -e

# 添加以下行（每分钟检查一次）
* * * * * cd /root/market-maker-go && ./scripts/health_check.sh >> logs/cron_health.log 2>&1
```

## 故障处理流程

### 1. 发现异常

当健康检查报告异常时：

```bash
# 立即查看最近日志
tail -100 logs/phoenix_live.out

# 查找ERROR日志
grep "ERR" logs/phoenix_live.out | tail -20

# 查看WebSocket状态
grep "WebSocket\|深度\|断流" logs/phoenix_live.out | tail -10
```

### 2. 诊断问题

**场景A：WebSocket断流**
```
症状: "深度数据停止更新" 或 "价格数据过期"
原因: WebSocket连接异常
操作: 重启进程
```

**场景B：订单停止更新**
```
症状: "订单长时间未更新"
原因: 可能撤单限制或API限制
操作: 检查API限速，检查撤单频率
```

**场景C：Panic崩溃**
```
症状: 日志中有 "panic" 或 "goroutine发生panic"
原因: 代码异常
操作: 查看panic堆栈，联系开发人员
```

### 3. 紧急处理

**立即止损**:
```bash
# 取消所有订单（保护本金）
./bin/emergency cancel-all

# 查看当前仓位
curl -s "https://fapi.binance.com/fapi/v2/positionRisk?symbol=ETHUSDC" \
  -H "X-MBX-APIKEY: YOUR_KEY" | jq '.'

# 如需平仓（手动确认风险）
# ./bin/emergency close-all
```

**重启进程**:
```bash
# 停止进程
./scripts/stop_live.sh

# 等待3秒
sleep 3

# 启动进程
./scripts/start_live.sh

# 持续监控5分钟
watch -n 1 "tail -20 logs/phoenix_live.out"
```

### 4. 验证恢复

重启后验证系统正常：

```bash
# 等待30秒后执行健康检查
sleep 30
./scripts/health_check.sh

# 查看订单更新
tail -f logs/phoenix_live.out | grep "订单已"

# 查看价格更新
tail -f logs/phoenix_live.out | grep "报价已生成"
```

## 监控建议

### 基础监控（必须）

1. **健康检查脚本**：每30-60秒执行一次
2. **日志监控**：持续tail日志，关注ERROR
3. **人工巡查**：每1-2小时检查一次

### 进阶监控（推荐）

1. **Prometheus + Grafana**：
   - 访问 http://localhost:9090 查看metrics
   - 配置告警规则

2. **告警通知**：
   - 集成企业微信/钉钉机器人
   - 发送关键告警到手机

3. **Dashboard**：
   - 实时查看订单、仓位、PnL

### 监控指标

关键指标及阈值：

| 指标 | 正常值 | 告警阈值 | 严重阈值 |
|------|--------|----------|----------|
| 日志更新间隔 | <10秒 | >30秒 | >60秒 |
| 价格更新间隔 | <3秒 | >5秒 | >10秒 |
| 活跃订单数 | 10-20个 | <5个 | 0个 |
| 订单更新间隔 | <60秒 | >120秒 | >300秒 |
| ERROR日志 | 0个 | >5个/分钟 | >10个/分钟 |

## 预防措施

### 配置优化

1. **降低报价间隔**（提高响应速度）:
```yaml
quote_interval_ms: 1000  # 1秒
```

2. **放宽防闪烁容差**（已优化）:
```go
tolerance = layerSpacing * 0.9  // 当前配置
```

3. **定期重启**（可选）:
```bash
# 每24小时重启一次（规避内存泄漏等问题）
0 4 * * * cd /root/market-maker-go && ./scripts/stop_live.sh && sleep 5 && ./scripts/start_live.sh
```

### 操作规范

1. ✅ 启动后持续观察10分钟
2. ✅ 每小时检查一次健康状态
3. ✅ 发现异常立即处理，不拖延
4. ✅ 重大行情前暂停做市（如非农数据、CPI公布）
5. ✅ 夜间减小仓位上限或暂停

### 风险控制

1. **资金管理**：
   - 实盘资金不超过可承受损失
   - 设置合理的netMax（如0.5 ETH）
   - 使用止损（config中的stop_loss_thresh）

2. **API密钥**：
   - 使用只读+交易权限，禁用提现
   - 定期轮换API密钥
   - IP白名单限制

3. **应急预案**：
   - 准备手动平仓方案
   - 记录交易所客服联系方式
   - 测试紧急脚本可用性

## 常见问题

### Q1: 健康检查总是报"日志长时间未更新"？

**A**: 可能是日志文件权限问题或进程确实假死。检查：
```bash
ls -l logs/phoenix_live.out
ps aux | grep phoenix
tail -f logs/phoenix_live.out  # 看是否有新日志
```

### Q2: 自动重启会不会误杀正常进程？

**A**: 有可能。建议：
- `AUTO_RESTART=false`（默认）
- 只在人工确认后手动重启
- 或设置`MAX_FAILURES=5`增加容错

### Q3: 如何测试健康检查？

**A**: 
```bash
# 测试脚本
./scripts/health_check.sh

# 模拟假死（停止进程但不删除PID文件）
kill -STOP $(cat run/phoenix_live.pid)
sleep 5
./scripts/health_check.sh  # 应报告异常

# 恢复进程
kill -CONT $(cat run/phoenix_live.pid)
```

### Q4: 健康检查占用太多资源？

**A**: 
- 将`CHECK_INTERVAL`从30秒增加到60或120秒
- 减少tail的行数（如从500改为200）
- 关闭不需要的检查项

## 总结

通过实施以上措施，我们显著提升了Phoenix做市商的稳定性和可靠性：

✅ **主动监控**：不再被动等待异常，而是主动检测  
✅ **快速告警**：3-5秒内发现WebSocket断流  
✅ **自动恢复**：Panic后自动重启goroutine  
✅ **运维工具**：健康检查脚本提供可操作的故障诊断

**记住**：做市是7x24小时运行的业务，监控和运维比开发更重要。定期检查、及时响应、持续优化是确保系统稳定的关键。

