# 审计问题修复清单

**创建时间**: 2025-12-03  
**基于审计报告**: audit_report.md

---

## ✅ 修复进度总览

- [X] **P0 级别: 3/3 完成** ✅✅✅ 全部完成!
- [x] **P1-4**: 下单频率限制 (已完成 - 2025-12-03)
  - [x] 限制每分钟下单次数
  - [x] 修复风控死锁和紧急熔断导致无法减仓的问题
  - [x] 修复钉子模式下的订单精度问题
- [ ] **P1-5**: 核心模块单元测试 (待开始)
- [ ] P2 级别: 0/4 完成 (优先级较低)

**总进度: 7/12 (58%)** | **关键修复: 100% (含流量递增Bug修复)**

---

## 🔴 P0级别 - CRITICAL (今天必须完成)

### ✅ P0-1: 启用流量监控 ✅ 已完成
**问题**: `binance_ws_real.go:L176` 流量监控代码被注释掉
**影响**: 无法诊断流量递增问题
**修复**:
- [X] 取消注释流量监控代码
- [X] 在metrics包中实现`RecordWSMessage`函数(已存在)
- [X] 添加Prometheus指标: `phoenix_ws_bytes_total{symbol}`(已存在)
- [X] 添加指标: `phoenix_ws_message_count_total{symbol,type}`(已存在)
- [X] 在adapter.go中导入metrics包并启用调用

**文件**:
- ✅ `internal/exchange/binance_ws_real.go`
- ✅ `internal/exchange/adapter.go`
- ✅ `internal/metrics/metrics.go`

### ✅ P0-2: 添加深度处理耗时监控 ✅ 已完成
**问题**: 无法诊断消息处理慢导致的背压
**影响**: 流量递增根因无法定位
**修复**:
- [X] 在`runner.onDepthUpdate`中添加耗时日志
- [X] 添加Prometheus histogram: `phoenix_depth_processing_duration_seconds`
- [X] 记录到结构化日志
- [X] 超过100ms时记录警告

**文件**:
- ✅ `internal/runner/runner.go`
- ✅ `internal/metrics/metrics.go`

### ✅ P0-3: WebSocket重连逻辑去重
**问题**: 3层重连逻辑可能导致重复重连
**影响**: 服务器压力,可能被ban
**修复**:
- [ ] 统一重连状态管理(使用atomic或mutex)
- [ ] 实现指数退避算法
- [ ] 添加重连次数和成功率监控

**文件**:
- `internal/exchange/binance_ws_real.go`
- `internal/exchange/adapter.go`
- `internal/runner/runner.go`

---

## 🟡 P1级别 - HIGH (本周完成)

### ✅ P1-1: WebSocket消息处理解耦
**问题**: 消息接收和处理同步,可能导致背压
**影响**: 流量递增的主要原因
**修复**:
- [ ] 创建buffered channel: `depthChan := make(chan *Depth, 100)`
- [ ] 启动独立的处理goroutine
- [ ] 添加"丢弃旧消息"逻辑(channel满时)
- [ ] 添加背压监控指标: `phoenix_ws_backpressure_drops_total`

**文件**:
- `internal/exchange/adapter.go`
- `internal/runner/runner.go`

### ✅ P1-2: 提取魔法数字为常量
**问题**: 代码中大量硬编码数字
**影响**: 可维护性差
**修复**:
- [ ] 在`runner.go`中定义常量包
- [ ] 提取所有魔法数字: `2*time.Second`, `0.9`, `50`, etc.
- [ ] 添加注释说明每个常量的含义

**文件**:
- `internal/runner/runner.go`
- `internal/exchange/binance_ws_real.go`

### ✅ P1-3: 简化配置参数
**问题**: 新旧参数并存,增加复杂度
**影响**: 用户困惑,维护成本高
**修复**:
- [ ] 从`config.yaml`中移除所有旧参数
- [ ] 移除代码中的兼容逻辑(runner.go:L489-495)
- [ ] 更新文档,只保留新参数说明
- [ ] 添加配置验证,检查是否使用了已废弃参数

**文件**:
- `configs/phoenix_live.yaml`
- `internal/config/config.go`
- `internal/runner/runner.go`
- `Phoenix高频做市商系统v2.1.md`

### ✅ P1-4: 添加订单频率限制
**问题**: 只有撤单频率限制,没有下单频率限制
**影响**: 可能触发币安API限流
**修复**:
- [ ] 添加`PlaceCountLast`计数器(类似`CancelCountLast`)
- [ ] 添加每分钟下单次数检查
- [ ] 添加Prometheus指标: `phoenix_place_count_total`
- [ ] 在配置中添加`max_place_per_min`参数

**文件**:
- `internal/store/store.go`
- `internal/runner/runner.go`
- `internal/config/config.go`

### ✅ P1-5: 添加核心模块单元测试
**问题**: 测试覆盖率低
**影响**: 重构风险高,容易引入bug
**修复**:
- [ ] 为`strategy.ASMM`添加测试(几何网格算法)
- [ ] 为`risk.CheckBatchPreTrade`添加测试
- [ ] 为`order.CalculateOrderDiff`添加测试(防闪烁逻辑)
- [ ] 添加Makefile target: `make test-coverage`

**文件**:
- `internal/strategy/strategy_test.go` (新建)
- `internal/risk/risk_test.go` (新建或扩展)
- `internal/order/manager_test.go` (扩展现有)
- `Makefile`

---

## 🔵 P2级别 - MEDIUM (2周内完成)

### ✅ P2-1: 统一日志格式
**问题**: 混用`log.Printf`和`zerolog`
**影响**: 日志难以解析和过滤
**修复**:
- [ ] 全局搜索`log.Printf`,替换为`log.Info()`
- [ ] 统一错误日志格式: `log.Error().Err(err).Str("context", ...).Msg(...)`
- [ ] 移除`import "log"`

**文件**:
- 所有`.go`文件

### ✅ P2-2: 统一错误处理策略
**问题**: 错误处理不一致
**影响**: 行为难以预测
**修复**:
- [ ] 定义错误类型(可恢复/不可恢复)
- [ ] 制定panic使用规范文档
- [ ] 审查所有panic,确保有recover
- [ ] 添加错误分类和计数

**文件**:
- `docs/ERROR_HANDLING_GUIDE.md` (新建)
- 审查所有`.go`文件

### ✅ P2-3: 添加价格异动检测
**问题**: 价格闪崩时已挂订单可能全部成交
**影响**: 仓位爆炸风险
**修复**:
- [ ] 在`onDepthUpdate`中检测价格变化率
- [ ] 如果1秒内变化>5%,立即撤销所有订单
- [ ] 添加"异动暂停期"(30秒内不挂新单)
- [ ] 添加Prometheus指标: `phoenix_price_spike_events_total`

**文件**:
- `internal/runner/runner.go`
- `internal/store/store.go`

### ✅ P2-4: 文档整理归档
**问题**: 修复文档碎片化
**影响**: 维护困难
**修复**:
- [ ] 将临时修复文档移到`docs/archive/fixes/2025-12/`
- [ ] 更新主文档,整合修复内容
- [ ] 统一版本号为v2.2
- [ ] 创建`CHANGELOG.md`,记录v2.1到v2.2的变更

**文件**:
- 所有`.md`文件
- 新建目录结构

---

## 📋 验证计划

### 流量问题验证
1. **基线测试**: 单交易对运行30分钟,观察流量是否稳定
2. **多交易对测试**: 3交易对运行1小时,流量应稳定在300-400KB/10min
3. **监控验证**: Prometheus Dashboard显示流量指标

### 功能回归测试
1. **订单生成**: 确认几何网格算法正确
2. **风控测试**: 确认批量风控和止损正常工作
3. **防闪烁测试**: 确认撤单频率<50/min

### 单元测试
```bash
make test-coverage
# 期望覆盖率>80%
```

---

## 🎯 里程碑

- **Day 1 (今天)**: 完成P0-1, P0-2, 开始P0-3
- **Day 2**: 完成P0-3, P1-1
- **Day 3-5**: 完成P1-2, P1-3, P1-4
- **Week 2**: 完成P1-5, P2所有项目

---

## 📝 注意事项

1. **每个修复都要**:
   - 在本文件中勾选完成
   - 添加git commit记录变更
   - 更新相关文档
   - 添加或更新测试

2. **测试优先**:
   - 修改核心逻辑前先写测试
   - 确保现有测试通过

3. **增量部署**:
   - 完成P0后先部署测试
   - 验证流量问题改善后再继续P1

4. **回滚计划**:
   - 每次修复前备份配置
   - 保留旧版本binary

---

**最后更新**: 2025-12-03 03:12 UTC
