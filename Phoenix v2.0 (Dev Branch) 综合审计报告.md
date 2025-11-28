这是一份基于 **Project Phoenix v1.0 设计文档**、实盘观测现象以及前两位专家意见的综合审计报告。

这份报告旨在去除重复信息，深度合成问题的**技术根因（Root Cause）**，并提供一份工程化的**整改路线图**。

---

# Project Phoenix v1.0 (Dev Branch) 综合审计报告

审计对象：GitHub dev 分支代码 vs. Phoenix高频做市商系统v2.md 设计规范

审计结论：严重不合格 (Critical Failure)

当前状态建议：⛔ 立即停止实盘，回滚至模拟盘/回测环境。

---

## 一、 核心故障深度归因分析

结合实盘观测到的5个现象，我们发现当前代码并非简单的“Bug”，而是**架构层缺失**与**逻辑竞态**导致的系统性失效。

### 1. 挂单数量失控（>100单）

- **现象**：挂单远超设计的48单（24层×2边）。
    
- **设计规范**：每Symbol严格限制24层双边报价，撤单率<50/分钟。
    
- **综合根因**：**缺失“订单状态机”与原子性同步**。
    
    - **幽灵订单 (Ghost Orders)**：代码极可能采用了“先全撤后全挂”的粗暴逻辑，但在高频环境下，`PlaceOrder` 的网络延迟导致策略在收到“撤单成功”确认前就开启了下一轮计算，重复发单。
        
    - **状态同步缺失**：内存中的 `activeOrders` 列表没有与交易所返回的实时数据（WSS OrderUpdate）进行强校验。
        
    - **并发竞态**：如专家1所述，可能存在多个 Goroutine 同时运行策略实例，导致“双龙戏珠”，互不感知的多重下单。
        

### 2. 盘口订单堆叠与计算错误

- **现象**：多个价格相近的订单挤压在盘口，无梯队层次。
    
- **设计规范**：Near层动态分布，Spread基于波动率扩展；Far层固定在±4.8-12%。
    
- **综合根因**：**精度对齐失败与逻辑互斥失效**。
    
    - **TickSize 灾难**：未执行 `math.Round(Price / TickSize) * TickSize`。在数字货币中，`100.0001` 和 `100.0002` 被视为不同价格，导致本应重合的订单被分散挂出。
        
    - **模式冲突**：`Pinning`（钉单）模式与 `ASMM`（正常做市）模式本应互斥。代码可能在触发 Pinning 时，未 `return` 阻断 ASMM 逻辑，导致“钉子单”和“常规单”同时存在。
        

### 3. 几何网格区间异常（价格偏移）

- **现象**：网格挂到了离谱的价格区间。
    
- **设计规范**：远端16层应固定在 0.08手 @ ±4.8-12%。
    
- **综合根因**：**数学公式实现错误**。
    
    - **参数硬编码错误**：极有可能将百分比（0.12）与基数（1.0）混淆，或者在循环计算几何级数时，`Pow` 函数的指数基准计算错误。
        
    - **循环越界**：如专家2指出，可能错误地用 `i=0 to 24` 循环了所有层级，而非将 `Near` 和 `Far` 分开处理，导致 Far 层的指数增长叠加到了 Near 层上。
        

### 4. 盈亏归因缺失（黑盒运行）

- **现象**：不知道钱是怎么赚的，也不知怎么亏的。
    
- **设计规范**：明确要求 metrics 包含 `mm_funding_pnl_acc` 等指标，且需上报 Grafana。
    
- **综合根因**：**数据流断裂 (Broken Pipeline)**。
    
    - **WSS 回调未挂载**：`Exchange` 层收到了成交推送，但没有通过 `Channel` 或 `Callback` 传递给 `Risk` 或 `Metrics` 模块。
        
    - **PnL 计算逻辑缺失**：只记录了“成交了”，未计算“成交价格 vs 开仓均价”的差值，导致系统无法生成 PnL 数据。
        

### 5. 巨额持仓下风控“装死” (最致命风险)

- **现象**：持仓很大时，不触发减仓（Grinding）或钉单（Pinning）。
    
- **设计规范**：`|pos| > 70%` 触发 Pinning，`|pos| > 87%` 触发 Grinding (Taker 7.5%)。
    
- **综合根因**：**死锁机制与数据滞后**。
    
    - **逻辑死锁 (Deadlock)**：这是最危险的。当 `TotalNotional` 或 `NetMax` 超限时，风控模块可能直接返回 `BlockAllOrders`。正确的逻辑应该是 **"BlockOpen, AllowClose"**（禁止开仓，允许平仓）。现在的逻辑把“平仓单/减仓单”也拦截了，导致持仓被锁死。
        
    - **数据源错误**：如专家2敏锐指出，Position 更新可能依赖 REST API 轮询（延迟高），而非 WSS 推送。在高频下，3秒的轮询延迟足以让策略在错误仓位下继续狂奔。
        

---

## 二、 必须补全的架构缺失 (Architectural Gaps)

除Bug修复外，当前代码结构缺少以下核心模块，必须补全才能达到设计文档要求的“生产级”标准：

1. **Order Manager (OM) 层**：
    
    - **缺失现状**：策略直接调 API 下单。
        
    - **补全方案**：在 `Strategy` 和 `Exchange` 之间增加 OM 层。负责：`Diff` 计算（只修改有差异的订单）、本地 `OrderBook` 维护、以及 `Pending` 状态管理。
        
2. **原子化风控守卫 (Atomic Risk Guard)**：
    
    - **缺失现状**：风控是“事后诸葛亮”或“全盘封杀”。
        
    - **补全方案**：风控必须作为中间件 `Middleware` 存在，对每一个发出的 `Quote` 进行 `Pre-Trade` 校验（是否增加敞口？是否超过 MaxNotional？）。
        
3. **灾难恢复快照 (Crash Recovery)**：
    
    - **缺失现状**：重启后内存状态丢失。
        
    - **补全方案**：实现 `store` 模块的 `LoadSnapshot()`，重启时必须读取 `/tmp/phoenix_snapshot.json` 恢复之前的 PnL 累加器和持仓状态。
        

---

## 三、 紧急整改路线图 (Remediation Roadmap)

请开发团队严格按照以下优先级执行，**P0 未解决前严禁启动实盘**。

### 🔴 P0：熔断与防爆仓（立即执行）

1. **修复风控死锁**：修改 `risk/guard.go`，当超限时，放行 `ReduceOnly` (只减仓) 订单，严禁拦截 Grinding 策略发出的平仓单。
    
2. **强制单例锁**：在 `cmd/runner/main.go` 增加进程锁或互斥锁，确保每个 Symbol 只有一个 Strategy Goroutine 在运行。
    
3. **订单溢出熔断**：
    
    Go
    
    ```
    // 伪代码示例
    if len(activeOrders) > 50 {
        emergency.CancelAll()
        log.Fatal("Order leak detected: >50 orders")
    }
    ```
    

### 🟠 P1：核心逻辑修复（24小时内）

1. **实现 Order Diff 机制**：停止全撤全挂。逻辑改为：`CurrentOrders` vs `DesiredQuotes` 对比，只操作差异部分。
    
2. **修正数学公式**：
    
    - 对齐 TickSize：`price = math.Round(rawPrice / tickSize) * tickSize`。
        
    - 修复 Far Layer 几何级数公式，并编写单元测试验证价格区间严格落在 ±4.8-12%。
        
3. **WSS 驱动仓位**：废弃 REST 轮询仓位，必须使用 Binance WSS `ACCOUNT_UPDATE` 事件实时更新内存中的 `SymbolState`。
    

### 🔵 P2：可观测性与合规（48小时内）

1. **链路补全**：打通 `Exchange.OnFill` -> `Risk.OnFill` -> `Metrics.Update` 的数据链路。
    
2. **结构化日志**：确保每一笔 Fill 都记录 `Spread` 收益还是 `Funding` 收益，并写入 CSV 用于盘后分析。
    
3. **覆盖率测试**：执行 `go test -cover`，重点补充 `risk_test.go` 中的边界条件测试（模拟持仓 99% 时的行为）。
    

---

最终建议：

Project Phoenix 的文档设计是完善的，但工程实现目前处于“草稿”阶段。请依照此审计意见书进行推倒重构（Refactor），尤其是 Order Manager 和 Risk 模块，而不是修修补补。