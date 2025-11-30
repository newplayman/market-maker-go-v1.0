# 🔴 严重问题：持仓时买1卖1价差异常

## 问题描述

**用户观察**：
- 持仓状态：约3层（估计0.021 ETH）
- 买1卖1价差：约**10 USDT** ❌❌❌
- 正常预期：约**2.4 USDT** ✅

## 问题严重性

**🔴 严重级别：高**

### 影响
1. **无法有效平仓**：10U价差意味着需要价格移动10U才能盈利平仓
2. **错失套利机会**：正常做市商价差应<3U
3. **竞争力丧失**：其他做市商价差2-3U，我们10U无法成交
4. **库存积压风险**：无法及时平仓导致库存堆积

## 根本原因分析

### 假设1：错误进入Pinning模式 ⚠️ 最可能

**Pinning模式的问题**：

代码 `internal/strategy/strategy.go:566-580`：
```go
if pos > 0 {
    // 多头仓位：钉在卖价
    sellQuotes = append(sellQuotes, Quote{
        Price: bestAsk,  // 卖单钉在bestAsk
        Size:  pinSize,
        Layer: 0,
    })
    // ❌ 没有生成近端买单！
}
```

**Pinning模式下远端买单**：
```go
// 第一个买单在 mid - 4.8%
buyPrice = mid * (1 - 0.048)
         = 3000 * 0.952
         = 2856 USDT
```

**结果**：
```
卖1（bestAsk）: 3005 USDT
买1（远端层）:  2856 USDT
价差: 149 USDT ❌❌❌
```

**触发条件**：
```go
math.Abs(pos) / cfg.NetMax > cfg.PinningThresh

当前配置：
- pinning_thresh: 0.5 (50%)
- net_max: 0.30

触发条件：pos > 0.15 ETH
```

**如果持仓0.021 ETH**：
```
0.021 / 0.30 = 7% < 50% → 不应该触发
```

**可能原因**：
1. **配置被错误修改**：`pinning_thresh` 被改成 0.05 或更小
2. **NetMax计算错误**：实际使用的NetMax不是0.30
3. **仓位计算错误**：系统认为仓位更大

### 假设2：Inventory Skew过大 ❌ 不太可能

**正常计算**：
```
inventorySkew = -0.021/0.30 × 0.002 × 3000 = -0.42 USDT

reservation = 3000 - 0.42 = 2999.58
buy1 = 2999.58 - 1.2 = 2998.38
sell1 = 2999.58 + 1.2 = 3000.78
价差 = 2.4 USDT ✅ 正常
```

**除非**：`inventory_skew_coeff` 被改成很大的值（如0.2而不是0.002）

### 假设3：Spread计算错误 ❌ 不太可能

```go
spread = cfg.MinSpread * volScaling * mid

MinSpread = 0.0007
volScaling = 1.0-2.0 (波动率调整)

spread = 0.0007 × 1.5 × 3000 = 3.15 USDT

buy1 = reservation - spread/2 = 2999.58 - 1.575 = 2998.0
sell1 = reservation + spread/2 = 2999.58 + 1.575 = 3001.155
价差 = 3.15 USDT ✅ 接近正常
```

不会导致10U价差

## 诊断步骤

### 步骤1：检查当前配置

```bash
cd /root/market-maker-go
grep -E "(pinning_thresh|inventory_skew_coeff|net_max)" configs/phoenix_live.yaml
```

### 步骤2：实时监控模式切换

运行系统并观察：
```bash
timeout 60 ./bin/phoenix -config=configs/phoenix_live.yaml -log=info 2>&1 | grep -E "(模式|mode=|Pinning|pinning)"
```

### 步骤3：观察持仓和报价

```bash
timeout 60 ./bin/phoenix -config=configs/phoenix_live.yaml -log=info 2>&1 | grep -E "(报价已生成|pos=|buy1=|sell1=)"
```

期望看到：
```
pos=0.021
buy1=2998.x
sell1=3000.x
价差≈2.4U ✅
```

如果看到：
```
pos=0.021
buy1=2856.x  ← 距离mid 4.8%
sell1=3005.x
价差≈149U ❌ → 确认是Pinning模式错误触发
```

## 修复方案

### 方案A：修复Pinning模式生成逻辑（推荐）

**问题**：Pinning模式下，多头只生成卖单+远端层，缺少近端买单

**修复**：
```go
// internal/strategy/strategy.go:566-580
if pos > 0 {
    // 多头仓位：主要钉在卖价平仓，但保留近端买单防护
    
    // 1. 主要卖单：钉在bestAsk
    sellQuotes = append(sellQuotes, Quote{
        Price: bestAsk,
        Size:  pinSize,
        Layer: 0,
    })
    
    // 2. 【新增】近端买单防护（防止价格继续下跌）
    // 在mid下方1-2U处挂几层买单
    for i := 0; i < 3; i++ {  // 保留3层近端买单
        buyPrice := mid - cfg.GridStartOffset - float64(i)*cfg.GridFirstSpacing
        buyPrice = a.roundPrice(buyPrice, cfg.TickSize)
        buyQuotes = append(buyQuotes, Quote{
            Price: buyPrice,
            Size:  cfg.UnifiedLayerSize,
            Layer: i + 1,
        })
    }
}
```

### 方案B：调整Pinning触发阈值

**当前**：
```yaml
pinning_thresh: 0.5  # 50% NetMax触发
```

**建议**：
```yaml
pinning_thresh: 0.7  # 70% NetMax触发（更保守）
```

**理由**：
- 50%过早进入Pinning
- Pinning应该是接近风险上限时的被动策略
- 70%更符合文档"防闪烁"而非"防平仓"的定位

### 方案C：禁用Pinning（临时）

如果Pinning逻辑有问题，临时禁用：
```yaml
pinning_enabled: false  # 临时禁用
```

依赖Normal模式的仓位调整逻辑：
- 多头时减少买单、保持卖单 ✅
- 已经实现且经过验证

### 方案D：完整重写Pinning逻辑

**当前Pinning设计缺陷**：
1. 只考虑平仓方向（单向订单）
2. 忽略市场反向波动风险
3. 缺少近端对冲订单

**理想的Pinning逻辑**：
```
多头仓位Pinning：
- 主要卖单：钉在bestAsk（平仓）
- 次要卖单：远端2-3层（防止价格继续上涨错过平仓）
- 防护买单：近端1-3层（防止价格继续下跌加剧亏损）

目标：
1. 优先平仓（卖单钉在最优价）
2. 防止反向损失（买单防护）
3. 保持双向流动性（做市商职责）
```

## 立即行动

### 1. 验证问题（5分钟）

```bash
cd /root/market-maker-go

# 启动系统并观察
timeout 60 ./bin/phoenix -config=configs/phoenix_live.yaml -log=info 2>&1 | tee /tmp/diagnose.log

# 另一个终端查看
tail -f /tmp/diagnose.log | grep -E "(pos=|buy1=|sell1=|模式|Pinning)"
```

**等待出现持仓（手动成交或自然成交）**

**观察输出**，确认：
- [ ] 是否进入Pinning模式
- [ ] buy1和sell1的实际价格
- [ ] 价差是否异常

### 2. 临时修复（如果确认问题）

**方案1：禁用Pinning（最快）**
```bash
cd /root/market-maker-go
sed -i 's/pinning_enabled: true/pinning_enabled: false/' configs/phoenix_live.yaml
# 系统会热重载配置
```

**方案2：提高Pinning阈值**
```bash
sed -i 's/pinning_thresh: 0.5/pinning_thresh: 0.7/' configs/phoenix_live.yaml
```

### 3. 永久修复（修改代码）

修改 `internal/strategy/strategy.go` 的 `generatePinningQuotes` 函数，添加近端防护订单。

## 监控指标

修复后，持续监控：

```bash
# 每30秒检查一次价差
while true; do
    grep "报价已生成" logs/phoenix_live.out | tail -1 | \
    awk '{
        if ($0 ~ /buy1=/) {
            match($0, /buy1=([0-9.]+)/, buy);
            match($0, /sell1=([0-9.]+)/, sell);
            spread = sell[1] - buy[1];
            printf "买1: %.2f, 卖1: %.2f, 价差: %.2f USDT\n", buy[1], sell[1], spread;
            if (spread > 5.0) {
                printf "⚠️  价差过大！\n";
            }
        }
    }'
    sleep 30
done
```

**预期**：
- 无仓位时：价差 2.4-3.0 USDT ✅
- 有仓位时：价差 2.5-4.0 USDT ✅
- **绝对不能超过5.0 USDT** ❌

---

**状态**：🔴 问题待验证  
**优先级**：⚠️ 高（影响核心做市功能）  
**下一步**：立即运行诊断步骤1

