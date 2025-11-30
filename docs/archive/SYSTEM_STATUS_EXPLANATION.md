# Phoenix系统状态说明

## 当前情况

系统正处于**撤单频率保护**状态：
- 启动时间：16:17:14
- 触发保护时间：约16:17:30 (撤单数达到43)
- 当前时间：16:21+ 
- 已持续：约4分钟

## 问题分析

**撤单计数器重置机制**需要在执行新撤单时才会检查时间并重置。但系统现在处于保护状态（不执行新撤单），导致计数器无法重置，形成了一个"死锁"状态。

## 解决方案

### 方案1：重启系统（推荐）

最简单直接的方法：
```bash
cd /root/market-maker-go
./scripts/stop_live.sh   # 会自动撤单+平仓
./scripts/start_live.sh  # 重新启动
```

重启后系统会：
1. 清理所有旧订单
2. 撤单计数器重置为0
3. 重新建立订单网格
4. 使用新的配置参数（35%几何增长）

### 方案2：修复代码逻辑（长期方案）

需要修改`processSymbol`函数，在保护状态下也定期检查并重置计数器：

```go
// 在检查撤单频率时，同时检查是否应该重置计数器
if state != nil {
    state.Mu.Lock()
    // 检查是否超过1分钟，如果是则重置
    if time.Since(state.LastCancelReset) > time.Minute {
        state.CancelCountLast = 0
        state.LastCancelReset = time.Now()
    }
    cancelCount := state.CancelCountLast
    state.Mu.Unlock()
    
    if cancelCount >= int(float64(symCfg.MaxCancelPerMin)*0.8) {
        log.Warn()...
        return nil
    }
}
```

## 推荐行动

**立即执行方案1**，重启系统验证优化效果：

```bash
# 1. 停止并清理
cd /root/market-maker-go
./scripts/stop_live.sh

# 2. 重新启动
./scripts/start_live.sh

# 3. 等待30秒后查看挂单梯度
sleep 30
tail -100 logs/phoenix_live.out | grep "下单成功" | tail -30

# 4. 实时监控
tail -f logs/phoenix_live.out
```

## 预期结果

重启后应该看到：
- ✓ 买1/卖1距离mid约1-1.5U
- ✓ 层间距从1U几何递增（35%增长率）
- ✓ 远端最大层间距达到25-27U
- ✓ 撤单频率正常（初期会有一些，然后稳定）


