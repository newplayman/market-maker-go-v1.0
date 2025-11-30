# Phoenix 实盘监控报告

## 启动状态
- ✅ 系统启动成功（PID: 923626）
- ✅ 配置验证通过（pinning_thresh: 0.5, grinding_thresh: 0.7）
- ✅ WebSocket连接正常

## 风控机制工作状态

### 1. 批量风控检查 ✅ 正常工作
```
批量风控检查失败，调整报价数量
- 原始报价: 14层买单 + 14层卖单  
- 总买单量: 0.14 ETH (超过0.075 ETH限制)
- 调整后: 7层买单 + 7层卖单
- 调整后买单量: 0.07 ETH (符合限制)
```

**结论:** 批量风控正在按预期工作，成功限制最坏情况敞口在50% NetMax以内。

### 2. 动态报价调整 ✅ 正常工作
```
根据风控要求调整报价:
- original_buy_layers: 14 → adjusted_buy_layers: 7
- original_sell_layers: 14 → adjusted_sell_layers: 7  
- total_buy_size: 0.07 ETH
- total_sell_size: 0.07 ETH
- max_buy_allowed: 0.075 ETH ✅
- max_sell_allowed: 0.075 ETH ✅
```

**结论:** 动态调整机制正常，成功将挂单量控制在安全范围内。

## 发现的新问题

### ⚠️ Post-Only订单被拒绝

**错误信息:**
```
code: -5022
msg: "Due to the order could not be executed as maker, the Post Only order will be rejected"
```

**原因分析:**
1. 市场mid价格约 3000 USDT
2. 买单价格: 2997-3001 USDT
3. 配置的 `near_layer_start_offset: 0.00016` (约0.5U距离)太小
4. 订单价格太接近或穿过市场最优价格，会立即成交（taker）
5. 但系统使用Post-Only模式，要求只能作为maker挂单
6. **结果：所有订单被交易所拒绝**

## 问题根源

修复风控后，我们将配置调整为：
- `near_layer_start_offset: 0.00016` (目标是0.5U距离)
- `min_spread: 0.0003` (0.03%)

但这个配置在当前市场条件下太激进，导致订单太接近市场价格。

## 与文档规范的对比

**文档要求 (`Phoenix高频做市商系统v2.md`):**
> 买1卖1距离mid应在1.x U的差距

**当前配置实际效果:**
- 第一层约0.5U距离 ❌ (太近，导致Post-Only失败)
- 需要调整到约1-1.5U距离

## 建议的修复方案

### 方案1: 增大near_layer_start_offset (推荐)
```yaml
near_layer_start_offset: 0.0004  # 从0.00016增加到0.0004 (约1.2U @ 3000U)
min_spread: 0.0007               # 从0.0003增加到0.0007 (约2.1U总价差)
```

### 方案2: 调整策略层的sizeMultiplier
如果配置文件加载有缓存问题，需要：
1. 检查为什么策略层还在生成14层（应该是6+8=14，正确）
2. 但每层大小是0.01而不是0.006（配置可能未生效）

## 当前系统状态总结

### ✅ 工作正常的部分
1. 批量风控检查机制
2. 动态报价调整机制
3. 风控层次保护
4. 轻仓做市原则（0.07 ETH < 0.075 ETH）

### ❌ 需要修复的部分
1. **订单价格配置太激进**，导致Post-Only失败
2. 需要增大第一层距离，从0.5U调整到1-1.5U
3. 可能存在配置缓存问题（显示0.01而不是0.006）

## 下一步行动

1. **停止当前系统**
2. **调整配置参数**:
   - 增大 `near_layer_start_offset` 到 0.0004
   - 增大 `min_spread` 到 0.0007
3. **清理可能的配置缓存**
4. **重新启动系统**
5. **验证订单能够成功挂单**

---

**监控时间:** 2025-11-29 23:20-23:22
**系统PID:** 923626
**配置文件:** configs/phoenix_live.yaml

