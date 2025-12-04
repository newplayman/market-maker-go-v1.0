# 2025-12-04 Phoenix 系统改进与缺陷修复记录

> 目标：彻底排查“价格数据过期 / 假死 / 深度背压”系列问题，确保夜间无人值守也能稳定运行。

## 1. 当日问题快照

| 时间 (UTC) | 症状 | 根因定位 |
|-----------|------|----------|
| 03:23~03:33 | 日志持续打印 `【告警】价格数据过期`，所有交易对停更 | `SymbolState.Mu` 未及时释放，`UpdateMidPrice` 被堵，深度通道堆满导致 WS 推送失效 |
| 03:24~03:32 | `phoenix_depth_channel` 上升到 500/500，触发背压 | `runDepthProcessor` worker 全挂在 `state.Mu.Lock` 上，无法消费 |
| 03:32 以后 | Watchdog / runner panic 频发，WS 重连风暴 | Stale 检测阈值过长、缺乏主动排空/REST 兜底，导致循环重连但价格仍不过来 |

## 2. 关键修复项

### 2.1 Store 层锁治理
- 为 `SymbolState` 上的写锁统一改用 `defer state.Mu.Unlock()`；`UpdateMidPrice` 加入毫秒级等待监控（>200ms 立刻报警），确保任何路径异常都有日志。
- `SetActiveOrderCount`、`UpdatePendingOrders`、VPIN 启停等也全部加上 `defer`，避免短路径早返回遗忘解锁。

### 2.2 价格过期 & 深度背压闭环
- `STALE_PRICE_THRESHOLD_SECONDS` 调至 2s，与 Binance 盘口刷新频率匹配。
- `processSymbol` 中一旦检测到过期：`drainDepthChannel("stale_price")` → `refreshMidPriceFromREST()` → `tryReconnectWebSocket()`，先恢复 mid 再重连，彻底终结假死。
- 新增 `handleDepthBackpressure`：当深度 channel >70% 主动丢弃旧消息并补 mid，默认保留 40% 最新数据；所有丢弃/排空动作都会写 metrics + 警告。
- `UpdateDepthChannelMetrics` 在每次写 channel 都会更新，Prometheus 可直观看到 backlog。

### 2.3 极限风控与磨仓保障
- `applyPositionGuards` 引入三层防线：  
  1. `guard_block_ratio`：立即停止同向加仓；  
  2. `guard_flatten_ratio`：仅保留磨成本减仓挂单（最多 2 层并放大利量）；  
  3. `guard_liquidate_ratio`/`guard_pnl_stop_ratio`：触发 Reduce-Only 市价单，若被交易所拒绝则自动 fallback 到合适的限价单。
- 引入 `emergencyOrders` 追踪与冷却机制，防止重复触发；`handleEmergencyOrderUpdate` 负责在订单成交/取消时回收冷却。
- 所有下单前用 `enforceQuotePrecision` 再对齐 tick & minQty，杜绝精度问题导致的拒单。

### 2.4 指标 & 日志增强
- 新增 `metrics.UpdateGridLayerMetrics`，把买/卖网格层数暴露给 Prometheus，方便观察风控收敛情况。
- `UpdateMidPrice` 的慢锁警告、REST mid 刷新、背压主动排空等均在日志中清晰可见，定位路径一目了然。

## 3. 测试 & 验证

| 项目 | 结果 |
|------|------|
| `go test ./...` | ✅ 全部通过 |
| `make build` | ✅ 生成最新 `bin/phoenix` |
| `./scripts/start_live.sh` | ⚠️ 被环境检查提前阻断：未配置 `BINANCE_API_KEY/BINANCE_API_SECRET`，程序并未真正启动，需在生产主机导出密钥后重试 |
| `tail -f logs/phoenix_live.out` | 未能验证新的运行日志（原因同上） |

> **结论**：代码层面的修复已经编译、单测通过；部署验证必须在运行环境补齐 API 密钥后重新执行 `stop_live.sh -> start_live.sh` 流程。

## 4. 部署建议

1. **准备环境变量**：在生产 shell 中导出 `BINANCE_API_KEY`、`BINANCE_API_SECRET`（若有多账户请确认权限只在主机上可见）。
2. **平滑切换**：执行 `./scripts/stop_live.sh`（含双重紧急撤单） → `./scripts/start_live.sh`，确认新的 PID 写入 `run/phoenix_live.pid`。
3. **运行观察**：重点关注以下指标/日志：
   - `phoenix_depth_channel_length`、`phoenix_depth_channel_usage` 保持低位；
   - `已通过REST深度刷新mid价`、`主动清空深度channel…` 等兜底日志是否出现；
   - 极限风控触发时有无对应 `phoenix-guard-*` 订单 update。
4. **额外验证（可选）**：在测试网配置 `testnet: true` + 测试网密钥，跑一次回环确认无论面对长时间断流还是暴涨流量，都能自动恢复。

## 5. 后续待办

- [ ] 上线后抓取 30min 运行日志，确认 `Lock wait` 告警完全消失；
- [ ] 在 Grafana/Prometheus 看板中加入新的深度/网格指标，设置阈值告警；
- [ ] 若仍存在 Rest mid 失败的个别场景，考虑在 `refreshMidPriceFromREST` 中加重试 + fallback；
- [ ] 评估 `GuardEmergencySlice` 的默认值（目前 100%），根据交易员偏好可改为 50% 分批减仓。

---

以上记录覆盖了 2025-12-04 所有 debug / refactor / 验证操作，可作为今后回溯与审计的依据。免费下载立即来~（请将本文件纳入版本控制）。***
