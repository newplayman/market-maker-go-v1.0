# 多交易对实盘测试报告

**测试时间**: 2025-12-02 16:44  
**配置**: 5个永续合约同时运行  
**资金**: 1000 USDC, 20X杠杆

## 运行状态

### ✅ 成功运行的交易对（4/5）

| 交易对 | 活跃订单 | 挂单量 | VPIN状态 | 状态 |
|--------|---------|--------|----------|------|
| **ETHUSDC** | 14个 | 0.245 ETH/边 | ✅ 已启用 (bucket=50k) | 🟢 正常 |
| **SOLUSDC** | 14个 | 2.8 SOL/边 | ✅ 已启用 (bucket=30k) | 🟢 正常 |
| **BNBUSDC** | 14个 | 0.49 BNB/边 | ✅ 已启用 (bucket=20k) | 🟢 正常 |
| **XRPUSDC** | 14个 | 245 XRP/边 | ✅ 已启用 (bucket=100k) | 🟢 正常 |
| **BTCUSDC** | 0个 | - | ✅ 已启用 (bucket=100k) | 🔴 价差问题 |

### 📊 资金使用情况

| 指标 | 当前值 | 说明 |
|------|--------|------|
| 总挂单数 | 56/70计划 | 4个交易对正常 |
| 保证金占用 | ~156 USDC | 15.6%（安全） |
| 可用资金 | ~844 USDC | 84.4%缓冲 |
| WebSocket streams | 11个 | 5.5% (11/200限制) |
| 撤单速率 | <160/分钟 | <80% (160/200限制) |

## 问题与解决

### ❌ BTCUSDC 价差不足问题

**错误**:  
```
ERR 处理交易对失败 error="报价验证失败: 价差 0.0221% 小于最小值 0.1000%" symbol=BTCUSDC
```

**原因**:  
- BTC价格高（~90,573 USDC）
- `grid_start_offset=40` USDT 相对价格太小
- 实际价差: 40/90573 = 0.044% < min_spread(0.1%)

**待修复方案**:
1. **增加grid_start_offset**: 40 → 100 USDT
2. **或降低min_spread**: 0.1% → 0.05%

## VPIN功能验证

所有5个交易对的VPIN均已成功启用：

```
2025-12-02T16:43:44Z INF VPIN已为交易对启用 bucket_size=50000 num_buckets=50 symbol=ETHUSDC threshold=0.7
2025-12-02T16:43:44Z INF VPIN已为交易对启用 bucket_size=100000 num_buckets=50 symbol=BTCUSDC threshold=0.7
2025-12-02T16:43:44Z INF VPIN已为交易对启用 bucket_size=30000 num_buckets=50 symbol=SOLUSDC threshold=0.7
2025-12-02T16:43:44Z INF VPIN已为交易对启用 bucket_size=20000 num_buckets=50 symbol=BNBUSDC threshold=0.7
2025-12-02T16:43:44Z INF VPIN已为交易对启用 bucket_size=100000 num_buckets=50 symbol=XRPUSDC threshold=0.7
```

**VPIN特性**:
- ✅ 每个交易对独立计算
- ✅ 根据币种流动性调整bucket_size
- ✅ 实时监控订单流毒性
- ⏳ 等待trade数据积累后触发

## 配置调整记录

### 关键修改

1. **全局配置**:
   - `total_notional_max`: 1000 → 4000（支持5交易对）
   - `net_max验证`: > 0.1 → > 0.001（支持BTC小仓位）

2. **per-symbol撤单限制**:
   - `max_cancel_per_min`: 200 → 40（5个×40=200全局）

3. **精度和层大小调整**:
   - SOL: min_qty 0.1 → 0.01, unified_layer_size 0.875 → 0.4
   - BNB: tick_size 0.01 → 0.1, unified_layer_size 0.175 → 0.07
   - XRP: unified_layer_size 175 → 35
   - BTC: unified_layer_size 0.0025 → 0.001

## 监控命令

```bash
# 查看所有交易对状态
tail -f logs/phoenix_live.out | grep -E "(TICKER_EVENT|active_orders|VPIN)"

# 查看成交情况
tail -f logs/phoenix_live.out | grep -E "(成交|FILL|订单已成交)"

# 查看VPIN触发
tail -f logs/phoenix_live.out | grep -i "vpin"

# 查看错误
tail -f logs/phoenix_live.out | grep ERR
```

## 下一步行动

1. ⏳ **修复BTC配置**: 增加grid_start_offset至100或调整min_spread
2. ⏳ **持续监控**: 观察30分钟，记录成交和VPIN触发
3. ⏳ **数据收集**: 等待VPIN积累足够数据（可能需要1-2小时）
4. ⏳ **性能验证**: 确认CPU、内存、撤单速率在安全范围

## 结论

**多交易对功能已成功实现！** 4/5交易对正常运行，VPIN全部启用。系统架构完全支持多交易对，资金压力和技术限制均在安全范围。BTC配置需要微调后可加入。

