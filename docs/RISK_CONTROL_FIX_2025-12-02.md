# Phoenix风控失效修复报告
## 修复日期：2025-12-02

## 一、问题诊断

### 关键发现

1. **持仓失控**：实盘运行9小时后，持仓达到107层成交（4.29 ETH），远超net_max（1.50 ETH）的285.7%
2. **保证金过高**：观察到600+ USDC保证金占用（60%资金使用率），接近强平线
3. **风控失效**：风控机制完全未触发，最终导致强平

### 根本原因分析

#### 漏洞1：单笔风控绕过
`CheckPreTrade`只检查单笔订单后是否超标，每笔0.04 ETH看起来合规，但累积107笔就是4.29 ETH。

#### 漏洞2：批量风控失明
`CheckBatchPreTrade`只检查挂单风险（maxWorstCase=0.75 ETH），完全没检查当前持仓是否超标。

#### 漏洞3：Grinding触发太晚
- 触发点：60% net_max = 0.9 ETH（接近强平线）
- 还需要波动率<0.38%，市场剧烈波动时无法触发

#### 漏洞4：无兜底机制
系统缺少"持仓超标立即停机"的最后防线。

## 二、修复方案

### 修复1：增加持仓绝对值检查（最高优先级）

**文件**：`internal/risk/risk.go`
**位置**：`CheckPreTrade`函数第49行之后

```go
// 【修复1】先检查当前持仓是否已经超标 - 这是最关键的防线
// 如果持仓已超标，只允许减仓，严格禁止任何开仓操作
if math.Abs(currentPos) > symCfg.NetMax {
    isReducing := (currentPos > 0 && side == "SELL") || (currentPos < 0 && side == "BUY")
    if !isReducing {
        log.Error().
            Str("symbol", symbol).
            Float64("current_pos", currentPos).
            Float64("net_max", symCfg.NetMax).
            Float64("pos_ratio", math.Abs(currentPos)/symCfg.NetMax*100).
            Str("side", side).
            Float64("size", size).
            Msg("持仓已超标，禁止继续开仓")
        return fmt.Errorf("持仓%.4f已超netMax%.4f(%.1f%%)，禁止继续开仓", 
            math.Abs(currentPos), symCfg.NetMax, math.Abs(currentPos)/symCfg.NetMax*100)
    }
    // 仅允许减仓，记录警告日志
    log.Warn().
        Str("symbol", symbol).
        Float64("current_pos", currentPos).
        Float64("net_max", symCfg.NetMax).
        Float64("pos_ratio", math.Abs(currentPos)/symCfg.NetMax*100).
        Str("side", side).
        Float64("size", size).
        Msg("持仓超标，仅允许减仓操作")
}
```

**效果**：
- 持仓超过net_max后，立即禁止任何开仓操作
- 只允许减仓方向的订单通过
- 记录详细的错误和警告日志

### 修复2：降低Grinding触发阈值并放宽波动率限制

**文件**：`internal/strategy/grinding.go`
**位置**：`ShouldStartGrinding`函数第56-62行

**代码修改**：
```go
// 【修复2】放宽波动率限制：从0.38%提高到1%
// 原因：市场剧烈波动时更需要grinding来减仓，过于严格的波动率限制会导致风控失效
stdDev := a.store.PriceStdDev30m(symbol)
if stdDev >= 0.01 { // 1% 波动率阈值（从0.38%放宽）
    log.Debug().
        Str("symbol", symbol).
        Float64("std_dev", stdDev).
        Float64("position_ratio", positionRatio).
        Msg("波动率过大，暂不启动grinding")
    return false // 波动太大，不适合grinding
}

log.Info().
    Str("symbol", symbol).
    Float64("position_ratio", positionRatio).
    Float64("std_dev", stdDev).
    Msg("触发Grinding模式")
return true
```

**配置修改**：`configs/phoenix_live.yaml`
```yaml
grinding_thresh: 0.30  # 从0.60降低到0.30，更早启动减仓
```

**效果**：
- Grinding触发点从60%降低到30%（从0.9 ETH降到0.15 ETH）
- 波动率限制从0.38%放宽到1%，在更多市场情况下可以触发
- 更早启动主动减仓机制

### 修复3：增加紧急熔断机制

**文件**：`internal/strategy/strategy.go`
**位置**：`GenerateQuotes`函数第69行之后

```go
// 【修复3】紧急熔断机制：持仓超过80% NetMax时停止报价
// 这是最后一道防线，防止持仓失控导致强平
posRatio := math.Abs(pos) / symCfg.NetMax
if posRatio > 0.80 {
    log.Error().
        Str("symbol", symbol).
        Float64("pos", pos).
        Float64("net_max", symCfg.NetMax).
        Float64("pos_ratio", posRatio*100).
        Msg("【紧急熔断】持仓超过80% netMax，停止报价以防止持仓继续扩大")
    return nil, nil, fmt.Errorf("紧急熔断: 持仓使用率%.1f%%超过80%%阈值，停止报价", posRatio*100)
}

// 如果持仓超过50%，记录警告日志
if posRatio > 0.50 {
    log.Warn().
        Str("symbol", symbol).
        Float64("pos", pos).
        Float64("net_max", symCfg.NetMax).
        Float64("pos_ratio", posRatio*100).
        Msg("【风控警告】持仓已超过50% netMax，需要注意风险")
}
```

**效果**：
- 持仓达到80% net_max时立即停止生成新报价
- 持仓达到50% net_max时记录警告日志
- 提供多层级的风控预警机制

### 修复4：降低net_max配置

**文件**：`configs/phoenix_live.yaml`

**修改前**：
```yaml
net_max: 1.50  # 最大净仓位 1.50 ETH (~900 USDC @ 3000U)
```

**修改后**：
```yaml
net_max: 0.50  # 最大净仓位 0.50 ETH (从1.50降低到0.50，提高安全性)
```

**效果**：
- 最大持仓从1.50 ETH降低到0.50 ETH（降低67%）
- 最大保证金需求从75 USDC降低到25 USDC（2.5%资金使用率）
- 大幅提高安全边际，降低强平风险

## 三、风控层级体系（基于新配置）

假设ETH价格为2800 USDC，资金1000 USDC，20倍杠杆：

### 第一层：Grinding模式（30% net_max）
- **触发点**：0.15 ETH
- **名义价值**：420 USDC
- **保证金**：21 USDC（2.1%资金）
- **动作**：启动主动减仓，生成grinding报价

### 第二层：Pinning模式（50% net_max）
- **触发点**：0.25 ETH
- **名义价值**：700 USDC
- **保证金**：35 USDC（3.5%资金）
- **动作**：钉在最优价格被动等待成交 + 记录警告日志

### 第三层：紧急熔断（80% net_max）
- **触发点**：0.40 ETH
- **名义价值**：1120 USDC
- **保证金**：56 USDC（5.6%资金）
- **动作**：停止生成新报价，只保留现有订单

### 第四层：强制减仓（100% net_max）
- **触发点**：0.50 ETH
- **名义价值**：1400 USDC
- **保证金**：70 USDC（7%资金）
- **动作**：禁止任何开仓操作，只允许减仓

## 四、修复效果预测

### 单边行情模拟（ETH从2800跌到2700，-3.6%）

#### 修复前（旧配置）：
1. 持仓累积到-4.29 ETH（107层成交）
2. 保证金：600 USDC（60%资金）
3. Grinding未触发（波动率>0.38%）
4. **结果**：继续亏损→强平

#### 修复后（新配置）：
1. 持仓累积到-0.15 ETH（约4层成交）
2. **触发Grinding**（30%阈值，波动率限制放宽）
3. 持仓累积到-0.25 ETH
4. **触发Pinning + 警告**（50%阈值）
5. 持仓累积到-0.40 ETH
6. **触发紧急熔断**（80%阈值，停止报价）
7. 如果持仓继续增加到-0.50 ETH
8. **强制只允许减仓**（100%阈值）

**效果对比**：
- 最大持仓从4.29 ETH降低到0.50 ETH（降低88%）
- 最大保证金从600 USDC降低到70 USDC（降低88%）
- 资金使用率从60%降低到7%
- **避免强平风险**

## 五、验证建议

### 1. 代码层面
- ✅ 增加持仓绝对值检查
- ✅ 放宽grinding波动率限制
- ✅ 增加紧急熔断机制
- ✅ 增加多层级警告日志

### 2. 配置层面
- ✅ net_max从1.50降低到0.50
- ✅ grinding_thresh从0.60降低到0.30
- ✅ 更新配置注释和说明

### 3. 回测验证
建议使用历史强平前的数据进行回测：
1. 确认30%阈值能及时触发grinding
2. 确认80%熔断能阻止持仓继续扩大
3. 确认新配置不会导致过度减仓

### 4. 实盘测试
**强烈建议**：
1. 使用小额资金（50-100 USDC）进行测试
2. 手动模拟单边行情，验证风控触发
3. 监控日志中的警告和错误信息
4. 确认grinding和熔断机制正常工作

## 六、风险提示

1. **net_max降低**可能导致：
   - 成交机会减少
   - 收益降低
   - 但风险大幅降低，适合1000 USDC本金

2. **grinding_thresh降低**可能导致：
   - 更频繁进入grinding模式
   - 主动减仓增加手续费（但有Maker折扣）
   - 但能更早控制风险

3. **紧急熔断**可能导致：
   - 停止报价后失去做市收益
   - 但能避免强平损失

## 七、后续优化建议

1. **动态风控**：根据市场波动率和资金使用率动态调整net_max
2. **智能减仓**：在grinding模式下，根据市场深度智能调整减仓速度
3. **实时监控**：开发Web Dashboard实时显示风控状态
4. **告警系统**：接入钉钉/Telegram，持仓超50%时推送告警

## 八、修改文件清单

1. ✅ `internal/risk/risk.go` - 增加持仓绝对值检查
2. ✅ `internal/strategy/grinding.go` - 放宽波动率限制并增加日志
3. ✅ `internal/strategy/strategy.go` - 增加紧急熔断机制
4. ✅ `configs/phoenix_live.yaml` - 降低net_max和grinding_thresh
5. ✅ `docs/RISK_CONTROL_FIX_2025-12-02.md` - 本修复报告

## 九、部署建议

### 停止现有进程
```bash
cd /root/market-maker-go
./scripts/stop_live.sh
```

### 重新编译
```bash
make build
```

### 备份配置和数据
```bash
cp configs/phoenix_live.yaml configs/phoenix_live.yaml.backup
cp data/snapshot_mainnet.json data/snapshot_mainnet.json.backup
```

### 启动新版本
```bash
./scripts/start_live.sh
```

### 监控日志
```bash
tail -f logs/phoenix_live.out | grep -E "风控|熔断|Grinding|警告"
```

---

**修复完成时间**：2025-12-02
**修复验证状态**：待部署测试
**预期效果**：大幅降低强平风险，提高系统安全性

