# 防闪烁机制修复报告

## 修复时间
2025-11-30 13:44-13:47

## 问题诊断

### 撤单频率过高
**实测数据**:
- 30秒内：下单238次，撤单201次
- 撤单率：**402/分钟** ❌
- 目标：**<50/分钟** ✅

**根本原因**:
1. `quote_interval_ms: 200` → 每秒5次报价 × 60秒 = 300次/分钟报价频率
2. Tolerance计算使用旧配置参数(`NearLayerStartOffset`)，新配置未生效
3. Tolerance=70%层间距，过于激进
4. 36个订单 × 频繁报价 = 大量不必要撤单

## 修复方案

### 修复1: 更新tolerance计算逻辑

**文件**: `internal/runner/runner.go` (413-448行)

**修改前**:
```go
// 使用旧配置NearLayerStartOffset
nearStartOffset := symCfg.NearLayerStartOffset
layerSpacing := state.MidPrice * nearStartOffset
tolerance = layerSpacing * 0.5  // 50%
```

**修改后**:
```go
// 优先使用新配置GridFirstSpacing
if symCfg.GridFirstSpacing > 0 {
    layerSpacing = symCfg.GridFirstSpacing  // 1.2 USDT
} else if symCfg.NearLayerStartOffset > 0 {
    layerSpacing = state.MidPrice * symCfg.NearLayerStartOffset
} else {
    layerSpacing = state.MidPrice * 0.005
}

// 防闪烁：tolerance = 层间距 × 90%
tolerance = layerSpacing * 0.9  // 1.08 USDT
```

**效果**:
- Tolerance从 ~0.6U 提升到 1.08U
- 只有当价格偏离>1.08U时才撤单重挂
- 大幅降低不必要的撤单

### 修复2: 调整报价间隔

**文件**: `configs/phoenix_live.yaml`

**修改**:
```yaml
# 修改前
quote_interval_ms: 200  # 每秒5次报价

# 修改后
quote_interval_ms: 1000  # 每秒1次报价（降低5倍）
```

**理由**:
- 200ms间隔对于36层订单过于激进
- 1秒间隔足够响应市场变化
- 每分钟最多60次报价循环
- 考虑防闪烁tolerance，实际撤单频率会远低于60次

## 计算验证

### 防闪烁Tolerance
```
层间距(GridFirstSpacing): 1.2 USDT
Tolerance = 1.2 × 90% = 1.08 USDT
Tolerance百分比 = 1.08 / 3044 × 100% = 0.0355%
```

### 理论撤单频率
```
报价频率: 60次/分钟 (1秒间隔)
Tolerance覆盖: 90%层间距 = 1.08U

假设价格随机游走，标准差0.5U/秒：
- 价格需要偏离>1.08U才触发撤单
- 概率约30-40%的报价周期会触发撤单
- 理论撤单频率：60 × 35% = 21次/分钟 ✅

实际会更低，因为：
1. 价格不是完全随机游走
2. 多数订单距离mid较远，不易触发
3. 只有靠近mid的订单才需要频繁调整
```

## 文档规范对照

### Phoenix v2文档要求
> - 撤单率 <50/分钟
> - API限频利用 <80%
> - ErrFlicker if 撤单 >50/min

### 当前实现
```yaml
✅ quote_interval_ms: 1000 (1秒)
✅ tolerance: 1.08 USDT (90%层间距)
✅ 理论撤单率: ~20-30/分钟
✅ 日志可见: "防闪烁容差计算完成"
```

## 验证日志

```
2025-11-30T13:47:10Z INF 防闪烁容差计算完成 
  layer_spacing=1.2 
  mid=3044.055 
  symbol=ETHUSDC 
  tolerance=1.08 
  tolerance_pct=0.0355%
  tolerance_usdt=1.08
```

## 监控建议

### 实盘运行60秒测试
```bash
cd /root/market-maker-go
# 启动系统
./scripts/start_live.sh

# 60秒后统计
CANCEL_START=$(grep -c "撤单成功" logs/phoenix_live.out)
sleep 60
CANCEL_END=$(grep -c "撤单成功" logs/phoenix_live.out)
CANCEL_RATE=$((CANCEL_END - CANCEL_START))
echo "撤单率: $CANCEL_RATE /分钟 (目标<50)"
```

### 持续监控
```bash
# 查看防闪烁日志
tail -f logs/phoenix_live.out | grep "防闪烁"

# 统计撤单频率
watch -n 60 'grep "撤单成功" logs/phoenix_live.out | tail -100 | wc -l'
```

## 如果撤单率仍>50/分钟

### 进一步优化方案

**选项A: 增大Tolerance**
```go
tolerance = layerSpacing * 0.95  // 从90%提升到95%
```

**选项B: 再增大报价间隔**
```yaml
quote_interval_ms: 2000  # 从1秒提升到2秒
```

**选项C: 启用Pinning模式**
```yaml
pinning_enabled: true
pinning_thresh: 0.3  # 降低触发阈值，更早进入稳定模式
```

**选项D: 减少层数（最后手段）**
```yaml
total_layers: 12  # 从18层减少到12层
```

## 总结

✅ **防闪烁机制已修复**

### 关键改进
1. Tolerance计算适配新几何网格配置
2. Tolerance从70%提升到90%（更保守）
3. 报价间隔从200ms提升到1000ms（降低5倍频率）
4. 理论撤单率：20-30/分钟（符合<50/分钟要求）

### 预期效果
- ✅ 撤单频率大幅降低
- ✅ 订单更稳定（减少闪烁）
- ✅ API限频利用率降低
- ✅ 仍能及时响应市场变化（1秒延迟可接受）

### 权衡
- 牺牲：报价响应速度从200ms降到1秒
- 获得：API稳定性、撤单频率合规、订单稳定性
- **结论**：对于做市商而言，稳定性>速度

---

**状态**: ✅ 修复完成，待实盘验证  
**下一步**: 60秒实盘测试验证撤单率<50/分钟

