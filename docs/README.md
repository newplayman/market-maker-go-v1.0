# Phoenix 高频做市商系统 v2.0

基于ASMM（Asymmetric Spread Market Making）策略的高频做市商系统，支持多交易对、智能库存管理和风险控制。

## 核心特性

- **ASMM策略**: 基于库存的非对称价差做市
- **钉子模式**: 仓位过大时主动平仓
- **磨仓模式**: 仓位适中时被动平仓
- **多层报价**: 近端密集 + 远端稀疏的挂单结构
- **实时风控**: 仓位限制、止损、撤单频率控制
- **Prometheus监控**: 完整的指标采集和可视化
- **热重载配置**: 无需重启即可更新配置

## 系统架构

```
Phoenix v2
├── cmd/runner/          # 主程序入口
├── internal/
│   ├── config/         # 配置管理（支持热重载）
│   ├── store/          # 状态存储（仓位、订单、市场数据）
│   ├── strategy/       # ASMM策略实现
│   ├── risk/           # 风控管理
│   ├── metrics/        # Prometheus监控
│   └── runner/         # 核心运行器
├── internal/exchange/  # 交易所API接口（集成自market-maker-go）
└── config.yaml         # 配置文件
```

**注**: Exchange API已从原项目(market-maker-go dev分支)成功集成，包含完整的Binance REST和WebSocket实现。详见 `EXCHANGE_API_INTEGRATION.md`。

## 快速开始

**📖 完整启动指南请查看 [QUICKSTART.md](QUICKSTART.md)**

### 快速步骤

1. **安装依赖**: `go mod download`
2. **配置**: `cp config.yaml.example config.yaml` 并编辑API密钥
3. **编译**: `make build`
4. **运行**: `./bin/phoenix -config config.yaml -log info`
5. **监控**: 访问 `http://localhost:9090/metrics`

详细说明、Docker部署、常见问题等请参考 [QUICKSTART.md](QUICKSTART.md)

## 配置说明

### 全局配置

```yaml
global:
  total_notional_max: 100000.0  # 总名义价值上限 (USD)
  quote_interval_ms: 500        # 报价间隔 (毫秒)
  api_key: "YOUR_KEY"           # Binance API Key
  api_secret: "YOUR_SECRET"     # Binance API Secret
  testnet: true                 # 是否使用测试网
  metrics_port: 9090            # 监控端口
  snapshot_path: "./data/snapshot.json"  # 快照路径
  snapshot_interval: 60         # 快照间隔 (秒)
```

### 交易对配置

```yaml
symbols:
  - symbol: "ETHUSDC"
    net_max: 10.0              # 最大净仓位
    min_spread: 0.0002         # 最小价差 (0.02%)
    base_layer_size: 0.1       # 基础挂单量
    near_layers: 3             # 近端层数
    far_layers: 5              # 远端层数
    pinning_enabled: true      # 启用钉子模式
    pinning_thresh: 0.6        # 钉子触发阈值 (60%)
    grinding_enabled: true     # 启用磨仓模式
    grinding_thresh: 0.4       # 磨仓触发阈值 (40%)
    stop_loss_thresh: 0.05     # 止损阈值 (5%)
```

## 策略说明

### ASMM策略

根据当前库存动态调整买卖价差：

- **库存为0**: 对称价差，买卖均衡
- **多头仓位**: 卖价更优，鼓励卖出
- **空头仓位**: 买价更优，鼓励买入

### 钉子模式 (Pinning)

当仓位超过阈值（如60%）时：
- 主动挂单平仓
- 价格更激进
- 快速降低风险敞口

### 磨仓模式 (Grinding)

当仓位适中（如40%）时：
- 被动等待成交
- 价格相对保守
- 逐步平衡库存

## 风控机制

1. **仓位限制**: 单交易对净仓位上限
2. **总敞口控制**: 所有交易对总名义价值上限
3. **止损**: 未实现亏损或回撤超过阈值时停止做市
4. **撤单频率**: 限制每分钟撤单次数，避免被交易所限制
5. **价格验证**: 检查报价合理性，防止异常订单

## 监控指标

### 仓位指标
- `phoenix_position_size`: 当前仓位
- `phoenix_position_notional`: 名义价值
- `phoenix_unrealized_pnl`: 未实现盈亏

### 交易指标
- `phoenix_fill_count_total`: 成交次数
- `phoenix_fill_volume_total`: 成交量
- `phoenix_total_pnl`: 累计盈亏

### 风控指标
- `phoenix_worst_case_long`: 最坏情况敞口
- `phoenix_max_drawdown`: 最大回撤
- `phoenix_cancel_rate`: 撤单频率

### 系统指标
- `phoenix_quote_generation_duration_seconds`: 报价生成耗时
- `phoenix_api_latency_seconds`: API延迟
- `phoenix_error_count_total`: 错误计数

## 开发

### 项目结构

- `config`: 配置管理，支持热重载
- `store`: 内存状态存储，定期快照持久化
- `strategy`: 策略逻辑，生成买卖报价
- `risk`: 风控检查，交易前后验证
- `metrics`: Prometheus指标采集
- `runner`: 核心运行器，协调各模块

### 扩展

要添加新的交易对，只需在 `config.yaml` 中添加配置即可，系统会自动初始化。

要修改策略参数，直接编辑配置文件，系统会热重载（无需重启）。

## 注意事项

1. **测试网先行**: 生产环境前务必在测试网充分测试
2. **风险管理**: 合理设置仓位上限和止损阈值
3. **监控告警**: 配置Prometheus告警规则
4. **日志审计**: 定期检查日志，发现异常
5. **API限制**: 注意交易所的频率限制

## 许可证

MIT License

## 联系方式

如有问题或建议，请提交 Issue。
