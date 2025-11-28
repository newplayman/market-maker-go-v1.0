# Phoenix v2 测试网使用指南

本指南将帮助你在Binance测试网环境中运行Phoenix做市系统。

---

## 📋 准备工作

### 1. 获取测试网API密钥

1. 访问 Binance Futures 测试网: https://testnet.binancefuture.com
2. 使用Google/GitHub账号登录（无需KYC）
3. 点击右上角头像 → API Management
4. 创建新的API密钥
5. 保存API Key和Secret Key（只显示一次）
6. 确保API权限包含"Enable Futures"

**测试网特点：**
- 免费的虚拟资金（USDT）
- 与生产环境API完全兼容
- 无真实资金风险

---

## 🚀 快速启动（3步）

### 步骤1: 配置API密钥

编辑 `config.testnet.yaml` 文件：

```bash
# 使用任何文本编辑器打开
open config.testnet.yaml  # macOS
# 或
nano config.testnet.yaml  # Linux
# 或
notepad config.testnet.yaml  # Windows
```

找到以下行并替换为你的真实密钥：

```yaml
api_key: "YOUR_TESTNET_API_KEY"        # 替换这里
api_secret: "YOUR_TESTNET_API_SECRET"  # 替换这里
```

### 步骤2: 运行启动脚本

```bash
./run_testnet.sh
```

脚本会自动：
- 检查配置文件
- 验证API密钥是否已配置
- 创建必要的目录
- 编译程序（如果需要）
- 启动系统

### 步骤3: 观察运行

系统启动后，你会看到类似以下的日志：

```
{"level":"info","message":"Phoenix高频做市商系统 v2.0 启动中..."}
{"level":"info","symbols":1,"total_notional_max":10000,"message":"配置加载成功"}
{"level":"info","testnet":true,"message":"初始化Binance连接"}
{"level":"info","symbol":"BTCUSDT","message":"交易对初始化完成"}
{"level":"info","message":"Phoenix系统启动完成"}
```

---

## 📊 监控系统运行

### 查看日志

日志会实时输出到终端，包括：

- **订单信息**: 下单、撤单、成交
- **仓位变化**: 净仓位、未实现盈亏
- **策略决策**: 报价生成、模式切换
- **风控触发**: 仓位限制、止损检查

关键日志示例：

```json
// 报价生成
{"level":"debug","symbol":"BTCUSDT","buy_quotes":3,"sell_quotes":3,"message":"报价已下达"}

// 订单成交
{"level":"info","symbol":"BTCUSDT","side":"BUY","qty":0.001,"price":50000,"message":"订单成交"}

// 仓位更新
{"level":"info","symbol":"BTCUSDT","net_position":0.003,"message":"仓位已更新"}

// 风控触发
{"level":"warn","symbol":"BTCUSDT","net_position":0.008,"max":0.01,"message":"接近仓位上限"}
```

### Prometheus监控

访问监控端点：
```bash
curl http://localhost:9090/metrics
```

关键指标：

```
# 报价生成次数
phoenix_quote_generation_total{symbol="BTCUSDT"} 150

# 当前净仓位
phoenix_position_net{symbol="BTCUSDT"} 0.003

# 订单下达次数
phoenix_order_placed_total{symbol="BTCUSDT",side="BUY"} 45

# 未实现盈亏
phoenix_unrealized_pnl{symbol="BTCUSDT"} 12.50
```

### 在测试网Web界面查看

访问: https://testnet.binancefuture.com

可以查看：
- 账户余额
- 持仓情况
- 订单历史
- 成交记录

---

## 🛑 停止系统

在运行系统的终端中按 `Ctrl+C`

系统会优雅关闭：
1. 停止接收新订单
2. 取消所有挂单
3. 保存状态快照到 `data/snapshot_testnet.json`
4. 关闭WebSocket连接

---

## ⚙️ 配置说明

### 当前测试配置（保守）

```yaml
symbols:
  - symbol: "BTCUSDT"
    net_max: 0.01              # 最大净仓位 0.01 BTC (约$500)
    base_layer_size: 0.001     # 每层挂单 0.001 BTC (约$50)
    near_layers: 3             # 近端3层
    far_layers: 3              # 远端3层
```

**预计行为：**
- 每次下单约 $50
- 最多持仓约 $500
- 双边挂单6层（买3层+卖3层）

### 调整参数

测试稳定后，可以逐步调整：

```yaml
# 增加仓位
net_max: 0.05              # 0.01 → 0.05 BTC

# 增加挂单量
base_layer_size: 0.002     # 0.001 → 0.002 BTC

# 增加挂单层数
near_layers: 5             # 3 → 5层
far_layers: 5              # 3 → 5层

# 缩小价差（更激进）
min_spread: 0.0003         # 0.05% → 0.03%
```

**重要：** 修改配置后需重启系统生效

---

## 🔍 常见场景

### 场景1: 系统正常做市

**日志示例：**
```
{"level":"debug","symbol":"BTCUSDT","buy_quotes":3,"sell_quotes":3,"message":"报价已下达"}
{"level":"info","symbol":"BTCUSDT","side":"BUY","qty":0.001,"message":"订单成交"}
{"level":"info","symbol":"BTCUSDT","net_position":0.001,"message":"仓位已更新"}
```

**说明：** 系统正常双边挂单，有订单成交

### 场景2: 钉子模式触发

**日志示例：**
```
{"level":"info","symbol":"BTCUSDT","net_position":0.007,"threshold":0.006,"message":"触发钉子模式"}
{"level":"info","symbol":"BTCUSDT","side":"SELL","message":"主动平仓挂单"}
```

**说明：** 净仓位超过60%（0.007/0.01），系统主动挂单平仓

### 场景3: 磨仓模式触发

**日志示例：**
```
{"level":"info","symbol":"BTCUSDT","net_position":0.005,"threshold":0.004,"message":"触发磨仓模式"}
{"level":"debug","symbol":"BTCUSDT","sell_quotes":5,"buy_quotes":3,"message":"调整挂单比例"}
```

**说明：** 净仓位超过40%，系统调整挂单策略被动平仓

### 场景4: 风控拒绝

**日志示例：**
```
{"level":"warn","symbol":"BTCUSDT","reason":"净仓位超限","message":"风控检查失败"}
{"level":"info","message":"取消本次报价"}
```

**说明：** 风控拦截，系统不会下单

---

## ❓ 常见问题

### Q: 启动后看不到订单？

**原因：**
1. API密钥配置错误
2. 网络连接问题
3. 测试网服务异常

**检查：**
```bash
# 查看日志中是否有错误
# 特别关注以下信息：
{"level":"error","error":"signature verification failed"}  # API密钥错误
{"level":"error","error":"timeout"}                        # 网络问题
```

### Q: 系统频繁撤单？

**原因：** 可能是价格波动大，系统调整报价

**正常行为：**
- 每1秒重新计算报价
- 价格变化超过最小价差时撤单重挂
- 仓位变化导致报价调整

### Q: 如何验证系统是否正常工作？

**检查清单：**
1. ✅ 日志中有 "Phoenix系统启动完成"
2. ✅ 日志中有 "报价已下达"
3. ✅ 测试网Web界面能看到挂单
4. ✅ Prometheus指标正常更新
5. ✅ 有订单成交记录

### Q: 测试多久合适？

**建议：**
- **最短：** 30分钟，观察基本功能
- **推荐：** 2-4小时，覆盖不同市场状态
- **理想：** 24小时，测试稳定性
- **完整：** 1周，测试各种场景

---

## 📈 性能基准

在测试网环境中，预期性能：

| 指标 | 预期值 |
|------|--------|
| 报价延迟 | < 100ms |
| CPU使用 | < 5% |
| 内存使用 | < 100MB |
| 网络带宽 | < 1Mbps |
| 撤单成功率 | > 95% |

---

## 🎯 测试目标

### 初级测试（30分钟）
- [x] 系统能正常启动
- [x] 能看到双边报价
- [x] 订单能正常下达和撤销
- [x] 能在Web界面看到订单

### 中级测试（2-4小时）
- [ ] 有订单成交
- [ ] 仓位正常更新
- [ ] 钉子/磨仓模式触发
- [ ] 风控正常工作
- [ ] 系统稳定运行无崩溃

### 高级测试（1天+）
- [ ] 多种市场状态测试
- [ ] 极端行情测试
- [ ] 长时间稳定性
- [ ] PnL变化符合预期
- [ ] 监控指标准确

---

## 🚨 紧急处理

### 立即停止系统

```bash
# 方法1: 优雅停止
按 Ctrl+C

# 方法2: 强制停止
pkill -9 phoenix

# 方法3: 查找并停止
ps aux | grep phoenix
kill -9 <PID>
```

### 清空所有挂单（测试网）

登录测试网Web界面：
1. 进入订单管理
2. 点击"全部撤销"

或使用API：
```bash
# 使用Binance测试网API撤销所有订单
# （需要你的API密钥）
```

### 查看系统状态

```bash
# 查看进程
ps aux | grep phoenix

# 查看监控
curl http://localhost:9090/metrics | grep phoenix

# 查看快照
cat data/snapshot_testnet.json | jq .
```

---

## 📝 测试记录模板

建议记录测试情况：

```markdown
### 测试记录

**日期：** 2025-11-28
**测试时长：** 2小时
**交易对：** BTCUSDT
**配置：** net_max=0.01, base_layer_size=0.001

**观察结果：**
- 订单数量：约xxx个
- 成交次数：约xxx次
- 最大仓位：0.xxx BTC
- 未实现盈亏：$xxx
- 系统稳定性：✅/❌

**发现问题：**
1. [描述问题]
2. [描述问题]

**改进建议：**
1. [改进建议]
2. [改进建议]
```

---

## ✅ 下一步

测试网验证通过后：

1. **参数优化**
   - 根据测试结果调整参数
   - 优化价差和挂单量
   - 测试更多交易对

2. **监控完善**
   - 配置Grafana仪表板
   - 设置告警规则
   - 建立监控流程

3. **生产准备**
   - 阅读 [DEPLOYMENT_READY.md](DEPLOYMENT_READY.md)
   - 准备生产配置
   - 制定应急预案

---

**祝测试顺利！** 🚀

如有问题，查看日志或参考 [QUICKSTART.md](QUICKSTART.md) 和 [PROJECT_STATUS.md](PROJECT_STATUS.md)
