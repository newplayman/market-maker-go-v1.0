Project Phoenix: 高频 USDC 永续合约做市系统版本: v1.0 (Phoenix 重构版, 2025-11-27)  
作者/架构师: Grok (xAI 高频量化工程师)  
状态: 生产级重构蓝图 - 推倒返工，坚不可摧  
许可证: Apache 2.0 (保留原项目 API 模块版权)

---

文档声明这份文档是 Project Phoenix 的完整开发说明书，作为项目重构的“宪法”。它基于原项目 (Round v0.7 main 分支) 的基础模块（如 API 接口），彻底重写策略、风控等核心逻辑。重构原则：模块化、不可变性、测试驱动、可观测性，目标是构建一个“永不爆仓、年化 100%+、容纳 $10M+ 资金”的生产级系统。

- 为什么重构：原 dev 分支代码混乱（多专家/工程师迭代导致逻辑纠缠、接口不稳、测试缺失），修改成本 > 维护成本 2x。保留 API (exchange/) 模块，避免重复造轮子。
- 重构范围：保留 exchange/ (API 封装) 和 metrics/ (监控)；重写 strategy/、risk/、store/、runner/；新增 multi-symbol 支持。
- 预期成果：单仓库、零依赖外部 DB/Redis、Go 1.21+、CI/CD 集成、99.9% uptime。
- 使用规则：任何后续开发/修改，必须 100% 遵守此文档。违规回滚 + 检讨报告。

---

一、项目目标1.1 业务目标

- 核心功能：在 Binance USDC-margined 永续合约（免 maker 费对，如 ETHUSDC、SOLUSDC、XRPUSDC 等）上运行高频做市，提供双边流动性，捕获 bid-ask spread + funding hedge 利润。
- 关键指标：
    - 收益率：年化 100%+ (小资金 200-5k USDC: 200-400%；大资金 5M-10M USDC: 100-150%)。
    - 风险控制：最大回撤 <25%；净仓硬帽 per-symbol 0.20 手 (名义 $40k)，全局 total_notional <$4M；永不爆仓 (stopLoss -20% 强制清仓)。
    - 规模支持：单账户多 symbol (8-15 个合约)，资金利用率 >85% (跨合约保证金共享)。
    - 运营效率：报价间隔 <200ms；撤单率 <50/分钟；fill rate >35%；API 限频利用 <80%。
- 非目标：不做预测交易 (纯中性做市)；不支持现货/期权对冲 (专注永续)；不集成 ML (规则 + 信号优先)。

1.2 技术目标

- 可靠性：99.9% uptime；WSS 全链路 (REST 降级)；进程单实例 (PID 锁 + healthcheck)。
- 可扩展性：插件式 symbol 配置；易加新策略 (e.g., ASMM → VPIN)；容器化部署 (Docker + K8s ready)。
- 可观测性：Prometheus/Grafana 全指标 (latency、fill rate、funding_pnl)；Loki 日志；Alertmanager 告警 (e.g., netMax 破)。  
    可观测性：Prometheus/Grafana 全指标 (延迟、填充率、funding_pnl)；Loki 日志；Alertmanager 告警 (例如，netMax 破)。
- 安全性：API key 加密 (env + Vault)；无明文日志；限频自适应。

1.3 成功标准

- 72h 连续实盘 (测试网)：无爆仓、无 429 错误、fill rate >35%、回撤 <10 USDC。
- 资金容量测试：模拟 $10M，收益率 >100% (backtest + 滑点模型)。

---

二、代码框架2.1 目录结构重构后仓库结构 (Go modules: github.com/newplayman/market-maker-go)：

```text
market-maker-phoenix/
├── cmd/                          # 可执行入口
│   ├── runner/                   # 主程序: 协调循环
│   │   └── main.go
│   ├── backtest/                 # 历史回测
│   │   └── main.go
│   └── sim/                      # 模拟行情测试
│       └── main.go
├── internal/                     # 内部模块 (私有)
│   ├── config/                   # 配置加载 (Viper + YAML)
│   │   └── config.go
│   ├── exchange/                 # API 接口 (保留原项目, 增强 WSS)
│   │   ├── binance_ws.go         # WSS UserStream + 深度流
│   │   ├── binance_rest.go       # REST 降级
│   │   └── types.go              # Order/Position 类型
│   ├── store/                    # 状态存储 (内存 + 快照)
│   │   └── store.go              # SymbolState, TotalNotional
│   ├── strategy/                 # 策略核心 (重写: ASMM + Pinning)
│   │   ├── asmm.go               # Adaptive Skewed MM
│   │   ├── pinning.go            # 防闪烁钉子模式
│   │   └── interfaces.go         # Strategy 接口
│   ├── risk/                     # 风控 (重写: 多层 + 全局)
│   │   ├── guard.go              # Pre/Post-trade 检查
│   │   ├── grinding.go           # 库存磨成本
│   │   └── global.go             # Total risk cap
│   └── metrics/                  # 监控 (保留原, 扩展指标)
│       └── prometheus.go         # mm_* 指标
├── scripts/                      # 运维脚本
│   ├── run_production.sh         # 生产启动 (Docker)
│   ├── emergency_stop.sh         # 紧急清仓
│   └── deploy_k8s.sh             # K8s 部署
├── test/                         # 测试 (单元 + 集成)
│   ├── strategy_test.go
│   ├── risk_test.go
│   └── integration_test.go       # Chaos + 多 symbol
├── configs/                      # 配置
│   └── phoenix.yaml              # 主配置 (多 symbol)
├── docs/                         # 文档
│   └── this_spec.md              # 此说明书
├── go.mod                        # Go 模块
└── Dockerfile                    # 容器化
```

2.2 模块分工

- cmd/: 入口点 (runner 为主, backtest/sim 为测试)。
- internal/: 核心业务 (不可外部导入)。
- scripts/: 运维 (Bash + Docker)。
- test/: 测试 (Go test + testify)。
- configs/: YAML 配置 (Viper 加载)。

2.3 数据流

```text
行情 (WSS 深度) → Store (更新 mid/position) → Strategy (生成报价) → Risk (校验) → Exchange (下单/撤单) → Metrics (上报) → Grafana
Funding (WSS) → Risk (bias + pnl_acc) → Global Risk (total_notional)
```

---

三、代码标准3.1 语言与环境

- Go 版本: 1.21+ (模块模式, go mod tidy)。
- 依赖: 最小化 - gorilla/websocket, prometheus/client_golang, viper, testify (go.mod pin 版本, e.g., websocket v1.5.0)。
- 构建: go build -ldflags="-s -w" (strip debug)；Docker multi-stage build。

3.2 编码规范

- 风格: gofmt + golangci-lint (enforce: all rules except funlen >200)。
- 命名: CamelCase (public), snake_case (private)；错误用 sentinel (ErrInvalidQuote)。
- 并发: 所有共享状态用 sync.RWMutex；channel 缓冲 (e.g., orderChan chan Order, buffer 100)。
- 日志: zerolog (JSON, level DEBUG/INFO/ERROR)；事件标签 (e.g., { "event": "quote_generated", "symbol": "ETHUSDC" })。  
    日志: zerolog (JSON, 级别 DEBUG/INFO/ERROR)；事件标签 (例如，{ "event": "quote_generated", "symbol": "ETHUSDC" })。
- 错误处理: 上下文传播 (ctx err)；无 panic (用 log.Fatal)；自定义 ErrQuoteFlicker 等。
- 常量: UPPER_CASE (e.g., DefaultQuoteInterval = 200 * time.Millisecond)。  
    常量: UPPER_CASE (例如，DefaultQuoteInterval = 200 * time.Millisecond)。

3.3 文档与注释

- Godoc: 每个 public 函数/类型 100% godoc 注释 (/// style)。
- TODO: 用 // TODO(phoenix-v1.0): 标记待办, 每周审。
- 变更: 每个 commit 用 Conventional Commits (feat:, fix:, refactor:) + CHANGELOG.md 自动生成。  
    变更: 每个提交使用 Conventional Commits (feat:, fix:, refactor:) + CHANGELOG.md 自动生成。

3.4 性能要求

- 延迟: 行情到下单 <100ms (p99)。
- 内存: <100MB (heap)；goroutine <500。  
    内存: <100MB (堆)；goroutine <500。
- 限频: 自适应 (WSS 10 msg/s, REST 2400 wt/min)。

---

四、模块详述4.1 config (配置加载)

- 功能: Viper 加载 YAML，支持 env override，多 symbol 数组。
- 接口规范:
    - type Config struct { Symbols []SymbolConfig; Global GlobalConfig }
    - func LoadConfig(path string) (*Config, error) - 返回 err if invalid (e.g., netMax <0)。  
        func LoadConfig(path string) (*Config, error) - 如果无效（例如，netMax <0），则返回 err。
    - SymbolConfig: { Symbol string, NetMax float64, MinSpread float64, TickSize float64 }。
- 规范: 验证 (e.g., netMax >0.1)；热重载 (fsnotify watcher)。

4.2 exchange (API 接口 - 保留原, 增强 WSS)

- 功能: Binance WSS (深度 + UserStream) + REST 降级；订单/仓位/ funding 查询。
- 接口规范:
    - type Exchange interface { PlaceOrder(ctx context.Context, order Order) error; CancelOrder(ctx context.Context, id string) error; GetPosition(symbol string) (Position, error); GetFundingRate(symbol string) (float64, error) }
    - Order: { Symbol string, Side string, Type string, Quantity float64, Price float64, ClientOrderID string } (唯一 ID: phoenix-{symbol}-{timestamp}-{seq})。  
        订单：{ 符号字符串, 方向字符串, 类型字符串, 数量 float64, 价格 float64, 客户订单 ID 字符串 }（唯一 ID：phoenix-{symbol}-{timestamp}-{seq}）。
    - Position: { Symbol string, Size float64, EntryPrice float64, UnrealizedPNL float64 }。
    - WSS: StartStream(ctx, callbacks) - callbacks: OnDepth, OnOrderUpdate, OnAccountUpdate, OnFunding。
- 规范: WSS 重连 (retries=5, backoff 3s)；REST 限频 (weight tracker)；错误: ErrRateLimit, ErrInvalidOrder。

4.3 store (状态存储)

- 功能: 内存状态 (per-symbol + global)；快照 (JSON 每 5min)。
- 接口规范:
    - type Store struct { mu sync.RWMutex; symbols map[string]*SymbolState; totalNotional atomic.Float64 }
    - SymbolState: { Position Position; PendingBuy/Sell float64; MidPrice float64; PriceHistory []float64 (ring buffer 3600s) }。
    - Methods: UpdatePosition(symbol, pos Position); GetWorstCaseLong(symbol string) float64; PriceStdDev30m(symbol string) float64; PredictedFunding(symbol string) float64 (EMA)。  
        方法：UpdatePosition(symbol, pos Position); GetWorstCaseLong(symbol string) float64; PriceStdDev30m(symbol string) float64; PredictedFunding(symbol string) float64 (EMA)。
    - Global: GetTotalNotional() float64; IsOverCap() bool (>$4M)。  
        全局：GetTotalNotional() float64; IsOverCap() bool (>$4M)。
- 规范: RLock 读, Lock 写；快照 to /tmp/phoenix_snapshot.json (crash 恢复)。

4.4 strategy (策略核心 - 重写)

- 功能: ASMM (Adaptive Skewed Market Making) + Pinning (防闪烁)；生成双边报价。
- 接口规范:
    - type Strategy interface { GenerateQuotes(ctx context.Context, symbol string) ([]Quote, []Quote, error); UpdateMetrics() }
    - Quote: { Price float64; Size float64; Layer int }。  
        引用：{ Price float64; Size float64; Layer int }。
    - ASMM: reservation = mid + inventorySkew + fundingBias；spread = minSpread * volScaling。  
        ASMM：reservation = mid + inventorySkew + fundingBias；spread = minSpread * volScaling。
    - Pinning: if |pos| >70% netMax, nail to bestBid/Ask (size *2.3)；near 8 layers dynamic, far 16 layers fixed 0.08 hand @ ±4.8-12%。  
        固定：如果 |pos| >70% netMax，钉在最佳 Bid/Ask（大小*2.3）；接近 8 层动态，远离 16 层固定 0.08 手 @ ±4.8-12%。
- 规范: 每 symbol 独立实例；输出校验 (size >= minQty)；ErrFlicker if 撤单 >50/min。

4.5 risk (风控 - 重写)

- 功能: Pre-trade (拒单) + Post-trade (收敛) + Global cap；Grinding (库存磨成本)。
- 接口规范:
    - type RiskGuard struct { ... }
    - Methods: ValidateQuote(quotes []Quote, symbol string) error; OnFill(fill Fill) ; StartGrinding(symbol string) ; CheckGlobal() error。  
        方法：ValidateQuote(quotes []Quote, symbol string) error; OnFill(fill Fill) ; StartGrinding(symbol string) ; CheckGlobal() error。
    - Fill: { Symbol string; Side string; Quantity float64; Price float64; PNL float64 }。  
        Fill: { 符号字符串；方向字符串；数量 float64；价格 float64；PNL float64 }。
    - Grinding: if |pos| >87% + stdDev <0.38%, taker 7.5% + maker reentry @ +4.2bps (size*2.1)。  
        磨刀石：如果 |pos| >87% + 标准差 <0.38%，则做市商 7.5% + 提供流动性者重新进入 @ +4.2bps（大小*2.1）。
    - Global: total_notional >$4M → all symbols pause new orders。  
        全球：总名义价值>$4M → 所有符号暂停新订单。
- 规范: 原子操作 (atomic.Float64 for pnl_acc)；冷却 (haltSeconds=300s)；ErrNetCapBreach 等。

4.6 metrics (监控 - 保留原, 扩展)

- 功能: Prometheus 指标 + Grafana 面板。
- 接口规范:
    - func RegisterMetrics(); func UpdateQuoteMetrics(symbol string, quotes []Quote); func UpdateRiskEvent(event string, value float64)
    - 指标: mm_position{symbol}, mm_fill_rate_5m{symbol}, mm_total_notional, mm_funding_pnl_acc, mm_pinning_active, mm_grind_count_total。
- 规范: Histogram for latency (buckets 50ms-1s)；Counter for events；Grafana json in /dashboards/。

---

五、测试策略5.1 测试类型

- 单元测试：每个方法 (go test -v, >90% 覆盖, testify assert)。
- 集成测试：端到端 (mock Exchange, chaos: 断网 15min + 滑点 0.5%)。
- 回测：历史 CSV (mids_sample.csv, 1 年 tick 数据, 滑点模型)。
- 压力测试：多 symbol 高频 (10 symbols, 1000 quotes/min)。

5.2 测试用例示例

- 策略单元 (strategy_test.go):
    
    go
    
    ```go
    func TestASMM_GenerateQuotes(t *testing.T) {
        cfg := &Config{...}
        s := NewASMM(cfg)
        quotes, _, err := s.GenerateQuotes(context.Background(), "ETHUSDC")
        assert.NoError(t, err)
        assert.Len(t, quotes, 24) // 8 near + 16 far
        assert.Greater(t, quotes[0].Size, 0.001) // min size
    }
    ```
    
- 风控集成 (risk_test.go):
    
    go
    
    ```go
    func TestRiskGuard_OnFill_OverCap(t *testing.T) {
        g := NewRiskGuard(cfg)
        fill := Fill{Symbol: "ETHUSDC", Side: "BUY", Quantity: 0.25}
        g.OnFill(fill)
        assert.True(t, g.IsPaused("ETHUSDC")) // pause after breach
    }
    ```
    
- Chaos 集成 (integration_test.go):
    - 模拟 WSS 断 15min：assert recovery <30s, no lost orders。  
        模拟 WSS 断 15min：断言恢复时间小于 30 秒，无订单丢失。
    - 多 symbol 滑点 0.5%：assert pnl_acc < -5 USDC/日。  
        多 symbol 滑点 0.5%：断言累计盈亏小于-5 USDC/日。

5.3 覆盖率要求

- go test -cover >90% (分支覆盖 >80%)。  
    go test -cover >90% (分支覆盖大于 80%)。
- CI: GitHub Actions (on push/PR: lint + test + cover)。

---

六、验收标准6.1 功能验收

- 启动：go run cmd/runner 10s 内 WSS connected, 指标上报。
- 报价：每 symbol 24 层双边单, fill rate >35% (72h 测试网)。
- 风控：模拟扫单, netMax 未破；grinding 触发后成本降 0.1 USDC/次。
- 多 symbol：8 symbols 运行, total_notional <$4M, funding hedge pnl_acc > -2 USDC/日。

6.2 性能验收

- 延迟 p99 <100ms (行情→下单)。
- CPU <20% (i3 CPU), 内存 <80MB。  
    CPU <20%（i3 CPU），内存<80MB。
- 限频：撤单 <50/min, 无 429。

6.3 可靠性验收

- 72h 连续：无 crash, uptime 100%。  
    72 小时连续：无崩溃，正常运行时间 100%。
- 崩溃恢复：kill -9 后 30s 重启, 仓位 0 (快照恢复)。
- 告警：netMax 破 → email/Slack。

6.4 部署验收

- Docker build <2min, run on K8s (1 pod)。  
    Docker 构建<2 分钟，在 K8s（1 个 pod）上运行。
- 配置热载：改 YAML 无重启。

---

七、运维与扩展7.1 部署

- 本地：bash scripts/run_production.sh (env: API_KEY=xxx)。
- 生产：Docker + K8s (deploy_k8s.sh: 1 replica, HPA on CPU 70%)。
- 监控：Prometheus (scrape :8080), Grafana (pre-built dashboards/phoenix.json), Loki (logs)。  
    监控：Prometheus (scrape :8080), Grafana (预构建面板/phoenix.json), Loki (日志)。

7.2 扩展指南

- 新 symbol：configs/symbols[] 加 {Symbol: "BTCUSDC", NetMax: 0.15}；backtest 验证。
- 新策略：impl Strategy interface；runner.LoadStrategy("asmm")。  
    新策略：实现 Strategy 接口；runner.LoadStrategy("asmm")。
- CI/CD：GitHub Actions (test + build + push Docker)。

7.3 风险与缓解

- 爆仓：多层风控 + 快照；每周 backtest。
- 限频：自适应 throttle (weight <2000/min)。  
    限频：自适应 throttle (重量 <2000/分钟)。
- 市场：周末减 symbol 到 5 个 (流动性 <50%)。

---

八、附录8.1 配置示例 (configs/phoenix.yaml)

yaml

```yaml
global:
  total_notional_max: 4000000  # $4M
  quote_interval_ms: 200
symbols:
  - symbol: ETHUSDC
    net_max: 0.20
    min_spread: 0.0006
  - symbol: SOLUSDC
    net_max: 1.8
    min_spread: 0.0012
```

8.2 风险免责

- 此架构基于 2025 Binance API；费率/限频变更需审。
- 实盘前：测试网 1 周 + 100 USDC 实盘 3 天。

结束语：Phoenix v1.0 是你的“坚不可摧”起点。严格执行，$10M 年化 100% 不是梦。任何疑问/变更，

@Grok

审。

---

签名：Grok, xAI 高频架构师  
版本历史：v1.0 - Initial Phoenix (2025-11-27)