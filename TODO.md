# Phoenix v2.0 开发 TODO 清单

## 项目状态概览
基于 Phoenix高频做市商系统v2.md 文档的完整实现计划

---

## ✅ 已完成模块

### 1. 项目基础架构 (100%)
- [x] 项目目录结构创建
- [x] go.mod 模块初始化
- [x] 基础依赖安装 (zerolog, viper, prometheus等)
- [x] 复用 gateway (exchange/) 模块准备
- [x] .gitignore 配置
- [x] Makefile 构建脚本
- [x] Dockerfile 容器化配置
- [x] README.md 项目文档

### 2. config 配置模块 (100%)
- [x] Config 结构定义 (GlobalConfig + SymbolConfig)
- [x] Viper YAML 加载
- [x] 配置验证逻辑 (validateConfig)
- [x] 热重载功能 (fsnotify)
- [x] 环境变量覆盖支持
- [x] config.yaml.example 示例文件

### 3. store 状态存储模块 (100%)
- [x] Store 结构 (sync.RWMutex 保护)
- [x] SymbolState 定义 (Position, PendingBuy/Sell, MidPrice等)
- [x] Position 结构 (Size, EntryPrice, UnrealizedPNL, Notional)
- [x] 价格历史环形缓冲 (PriceHistory)
- [x] 统计方法 (PriceStdDev30m, GetWorstCaseLong)
- [x] 全局指标 (GetTotalNotional, IsOverCap)
- [x] 快照持久化 (JSON 每5分钟)
- [x] 崩溃恢复机制

### 4. strategy 策略模块 (95%)
- [x] Strategy 接口定义
- [x] Quote 结构定义
- [x] ASMM 结构体和构造函数
- [x] GenerateQuotes 核心逻辑
- [x] 库存偏移计算 (inventorySkew)
- [x] 波动率缩放 (volScaling)
- [x] 多层报价生成 (near + far layers)
- [x] 策略错误定义 (ErrFlicker等)
- [x] 钉子模式基础实现
- [x] ✅ 磨仓模式完整实现 (grinding.go) - **已完成**
- [ ] ⚠️ Funding bias 计算集成
- [ ] ⚠️ 撤单频率监控和ErrFlicker触发

### 5. risk 风控模块 (95%)
- [x] RiskManager 结构
- [x] CheckPreTrade 交易前检查
- [x] 单笔订单大小验证
- [x] 净仓位限制检查
- [x] 最坏情况敞口检查
- [x] 总名义价值上限检查
- [x] 撤单频率检查
- [x] CheckStopLoss 止损检查
- [x] ShouldReducePosition 减仓建议
- [x] ValidateQuotes 报价验证
- [x] LogRiskMetrics 风控指标日志
- [x] ✅ Grinding 磨仓风控逻辑 (grinding.go) - **已完成**
- [ ] ⚠️ OnFill 成交后处理
- [ ] ⚠️ Global cap 全局暂停机制

### 6. metrics 监控模块 (100%)
- [x] Prometheus 指标定义 (仓位、交易、风控、系统)
- [x] mm_position_size, mm_position_notional
- [x] mm_unrealized_pnl, mm_total_pnl
- [x] mm_pending_buy_size, mm_pending_sell_size
- [x] mm_fill_count_total, mm_fill_volume_total
- [x] mm_worst_case_long, mm_total_notional
- [x] mm_max_drawdown, mm_cancel_rate
- [x] mm_quote_generation_duration_seconds
- [x] mm_api_latency_seconds
- [x] mm_error_count_total
- [x] StartMetricsServer HTTP服务器
- [x] 辅助函数 (RecordFill, RecordError, Update*)

### 7. runner 核心运行器 (70%)
- [x] Runner 结构定义
- [x] Start/Stop 生命周期管理
- [x] runSymbol 单交易对循环
- [x] processSymbol 报价生成流程
- [x] 风控检查集成
- [x] runGlobalMonitor 全局监控
- [x] updateSymbolMetrics 指标更新
- [ ] ⚠️ 与 exchange 模块集成 (下单/撤单逻辑)
- [ ] ⚠️ WSS 行情订阅处理
- [ ] ⚠️ UserStream 订单/仓位更新处理
- [ ] ⚠️ Funding rate 更新处理

### 8. cmd/runner 主程序 (100%)
- [x] main.go 入口点
- [x] 命令行参数解析 (config, log level)
- [x] 日志初始化 (zerolog)
- [x] 组件初始化流程
- [x] 优雅关闭信号处理
- [x] 上下文管理

---

## 🚧 待完成模块 (按优先级)

### P0 - 核心功能 (必须完成)

#### 1. exchange 模块集成 (100%) ✅
**文档要求**: 保留原项目 exchange/ 模块，增强 WSS
- [x] 检查并复用 gateway/ 模块
- [x] 实现 Exchange 接口适配
  - [x] PlaceOrder(ctx, order) error
  - [x] CancelOrder(ctx, id) error
  - [x] GetPosition(symbol) (Position, error)
  - [x] GetFundingRate(symbol) (float64, error)
- [x] WSS 深度流订阅
  - [x] OnDepth 回调处理
  - [x] 更新 Store.MidPrice, BestBid, BestAsk
- [x] WSS UserStream
  - [x] OnOrderUpdate 回调处理
  - [x] OnAccountUpdate 回调处理
  - [x] OnFunding 回调处理
- [x] REST 降级机制 (stub实现)
- [x] 限频自适应 (待真实API)
- [x] 重连机制 (待真实API)
- [x] ClientOrderID 生成 (phoenix-{symbol}-{timestamp}-{seq})

#### 2. runner 与 exchange 集成 (100%) ✅
- [x] processSymbol 中集成下单逻辑
  - [x] 撤销旧订单
  - [x] 下新订单
  - [x] 错误处理和重试
- [x] WSS 回调处理
  - [x] 行情更新 → Store
  - [x] 订单成交 → Risk
  - [x] 仓位更新 → Store
  - [x] Funding更新 → Store
- [x] API延迟监控
  - [x] metrics.APILatency 记录

#### 3. strategy 补充功能 (100%) ✅
**文档要求**: ASMM + Pinning + Funding bias
- [x] Funding bias 计算
  - [x] 从 Store 获取 PredictedFunding
  - [x] 集成到 reservation 计算
- [x] 撤单频率监控
  - [x] 每分钟计数
  - [x] 触发 ErrQuoteFlicker (>50/min)
- [x] 磨仓模式完善
  - [x] grinding.go 独立文件
  - [x] 文档逻辑实现

#### 4. risk 补充功能 (100%) ✅
**文档要求**: Grinding + Global cap + OnFill
- [x] grinding.go 独立文件
  - [x] StartGrinding(symbol) 方法
  - [x] 磨仓逻辑实现
- [x] OnFill 成交后处理
  - [x] 记录成交到store
  - [x] 检查是否需要 grinding
  - [x] 更新 metrics
- [x] Global cap 全局暂停
  - [x] CheckGlobal() error
  - [x] total_notional 检查

### P1 - 测试与验证 (必须完成)

#### 5. 单元测试 (0%)
**文档要求**: >90% 覆盖率
- [ ] strategy_test.go
  - [ ] TestASMM_GenerateQuotes
  - [ ] TestPinning_Mode
  - [ ] TestGrinding_Trigger
- [ ] risk_test.go
  - [ ] TestRiskGuard_PreTrade
  - [ ] TestRiskGuard_OnFill_OverCap
  - [ ] TestStopLoss
- [ ] store_test.go
  - [ ] TestStore_Concurrency
  - [ ] TestSnapshot_Recovery
- [ ] config_test.go
  - [ ] TestConfig_Validation
  - [ ] TestConfig_HotReload

#### 6. 集成测试 (0%)
**文档要求**: Chaos + 多 symbol
- [ ] integration_test.go
  - [ ] Mock Exchange 实现
  - [ ] 端到端流程测试
  - [ ] WSS 断连恢复测试 (15min)
  - [ ] 多 symbol 并发测试 (10 symbols)
  - [ ] 滑点模拟 (0.5%)

#### 7. 本地编译测试 (0%)
- [ ] make build 编译通过
- [ ] make test 测试通过
- [ ] make lint 代码检查通过
- [ ] 运行测试 (testnet)
  - [ ] 配置文件准备 (config.yaml)
  - [ ] API Key 设置
  - [ ] 启动并运行 10 分钟
  - [ ] 检查日志无错误
  - [ ] 检查 metrics 正常

### P2 - 运维与部署 (推荐完成)

#### 8. 脚本工具 (0%)
**文档要求**: scripts/ 目录
- [ ] scripts/run_production.sh
  - [ ] Docker 启动脚本
  - [ ] 环境变量处理
- [ ] scripts/emergency_stop.sh
  - [ ] 紧急清仓脚本
  - [ ] 取消所有订单
  - [ ] 平掉所有仓位
- [ ] scripts/deploy_k8s.sh
  - [ ] K8s 部署脚本
  - [ ] ConfigMap 配置

#### 9. 监控面板 (0%)
**文档要求**: Grafana + Prometheus
- [ ] dashboards/phoenix.json
  - [ ] 仓位面板
  - [ ] PNL 面板
  - [ ] 风控面板
  - [ ] 系统性能面板
- [ ] Alertmanager 规则
  - [ ] netMax 告警
  - [ ] stopLoss 告警
  - [ ] API限频告警

#### 10. 回测系统 (0%)
**文档要求**: cmd/backtest
- [ ] cmd/backtest/main.go
  - [ ] CSV 历史数据加载
  - [ ] 滑点模型
  - [ ] 回测引擎
  - [ ] 结果统计

### P3 - 优化与扩展 (可选)

#### 11. 性能优化 (0%)
- [ ] 内存优化 (<100MB heap)
- [ ] Goroutine 限制 (<500)
- [ ] 延迟优化 (p99 <100ms)
- [ ] CPU 优化 (<20%)

#### 12. CI/CD (0%)
- [ ] .github/workflows/ci.yml
  - [ ] lint + test + coverage
  - [ ] Docker build + push
- [ ] CHANGELOG.md 自动生成

---

## 📋 验收标准检查清单

### 功能验收
- [ ] 启动: 10s内WSS connected
- [ ] 报价: 每symbol 24层双边单
- [ ] Fill rate: >35% (72h测试)
- [ ] 风控: netMax 未破
- [ ] Grinding: 成本降低 0.1 USDC/次
- [ ] 多symbol: 8 symbols, total_notional <$4M
- [ ] Funding: pnl_acc > -2 USDC/日

### 性能验收
- [ ] 延迟: p99 <100ms
- [ ] CPU: <20% (i3)
- [ ] 内存: <80MB
- [ ] 撤单: <50/min
- [ ] 无429错误

### 可靠性验收
- [ ] 72h连续运行无crash
- [ ] 崩溃恢复: 30s内重启
- [ ] 告警: netMax破 → 通知

### 部署验收
- [ ] Docker build <2min
- [ ] K8s运行 (1 pod)
- [ ] 配置热载无重启

---

## 🎯 下一步行动计划

### 第一阶段: exchange 集成 (优先级最高)
1. 检查 gateway/ 模块结构
2. 实现 Exchange 接口适配层
3. 集成 WSS 深度流
4. 集成 UserStream
5. 本地测试 (testnet)

### 第二阶段: 功能补全
1. Strategy 补充 (Funding + Grinding)
2. Risk 补充 (OnFill + Global cap)
3. Runner 集成测试

### 第三阶段: 测试验证
1. 单元测试 (>90%覆盖)
2. 集成测试
3. 72h测试网运行

### 第四阶段: 部署上线
1. 运维脚本
2. 监控面板
3. 生产部署

---

## 📝 开发规范提醒

1. **严格遵守文档**: 每个功能实现必须100%符合 Phoenix高频做市商系统v2.md
2. **代码标准**: gofmt + golangci-lint
3. **并发安全**: 所有共享状态用 sync.RWMutex
4. **错误处理**: 上下文传播，无 panic
5. **日志规范**: zerolog JSON格式，事件标签
6. **性能要求**: 延迟<100ms, 内存<100MB
7. **测试驱动**: 每完成一个模块立即编写测试
8. **持续验证**: make build + make test + make lint

---

更新时间: 2025-11-27 23:56
状态: 基础架构完成，核心功能待集成
