# Phoenix v2 快速启动指南

## 系统要求

- Go 1.21+
- Docker (可选，用于容器化部署)
- Binance Futures账户及API密钥

## 步骤1: 配置API密钥

### 方式1: 使用配置文件 (推荐用于测试)

复制示例配置文件：
```bash
cp config.yaml.example config.yaml
```

编辑 `config.yaml`，填入你的API密钥：
```yaml
global:
  api_key: "你的API_KEY"
  api_secret: "你的API_SECRET"
  testnet: true  # 测试网设为true，生产环境设为false
```

### 方式2: 使用环境变量 (推荐用于生产)

```bash
export BINANCE_API_KEY="你的API_KEY"
export BINANCE_API_SECRET="你的API_SECRET"
```

## 步骤2: 配置交易参数

编辑 `config.yaml` 中的交易对配置：

```yaml
symbols:
  - symbol: "BTCUSDT"
    net_max: 0.5          # 最大净仓位（手数）
    min_spread: 0.0002    # 最小价差（0.02%）
    tick_size: 0.1        # 价格最小变动
    min_qty: 0.001        # 最小下单量
    base_layer_size: 0.01 # 基础层级挂单量
    near_layers: 3        # 近端层数
    far_layers: 5         # 远端层数
    pinning_enabled: true # 启用钉子模式
    grinding_enabled: true # 启用磨仓模式
```

## 步骤3: 编译程序

```bash
make build
```

编译完成后，可执行文件位于 `bin/phoenix`

## 步骤4: 运行系统

### 测试网环境（推荐首次使用）

确保 `config.yaml` 中 `testnet: true`

```bash
./bin/phoenix -config config.yaml -log info
```

### 生产环境

⚠️ **警告：仅在充分测试后使用生产环境！**

1. 修改配置文件：`testnet: false`
2. 使用环境变量配置API密钥（更安全）
3. 运行：

```bash
export BINANCE_API_KEY="生产API_KEY"
export BINANCE_API_SECRET="生产API_SECRET"
./bin/phoenix -config config.yaml -log info
```

## 步骤5: 监控系统

### 查看日志

系统会输出结构化日志，包括：
- 订单执行情况
- 仓位变化
- 风控触发
- 错误信息

### Prometheus监控

默认端口：`9090`

访问指标：
```bash
curl http://localhost:9090/metrics
```

关键指标：
- `phoenix_quote_generation_total` - 报价生成次数
- `phoenix_order_placed_total` - 订单下达次数
- `phoenix_position_net` - 净仓位
- `phoenix_risk_check_failures_total` - 风控失败次数

### Grafana仪表板（可选）

可以配合Grafana创建可视化监控面板。

## 步骤6: 停止系统

按 `Ctrl+C` 优雅关闭系统。

系统会：
1. 停止接收新订单
2. 取消所有挂单
3. 保存状态快照
4. 关闭连接

## 使用Docker运行

### 构建镜像

```bash
make docker-build
```

### 运行容器

```bash
docker run -d \
  --name phoenix \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/data:/app/data \
  -p 9090:9090 \
  -e BINANCE_API_KEY="你的KEY" \
  -e BINANCE_API_SECRET="你的SECRET" \
  phoenix:latest
```

### 查看日志

```bash
docker logs -f phoenix
```

## 测试网信息

### Binance Futures测试网

- Web界面: https://testnet.binancefuture.com
- 申请测试API: https://testnet.binancefuture.com/zh-CN/futures/BTCUSDT

测试网特点：
- 使用虚拟资金
- 与生产环境API完全兼容
- 适合策略测试和参数调优

## 常见问题

### Q: 如何验证API连接？

运行系统后，查看日志中是否有：
```
{"level":"info","message":"初始化Binance连接","testnet":true}
{"level":"info","symbol":"BTCUSDT","message":"交易对初始化完成"}
```

### Q: 如何调整做市参数？

编辑 `config.yaml` 中的交易对配置，重启系统生效。

关键参数：
- `net_max`: 控制最大仓位风险
- `min_spread`: 控制报价利润空间
- `near_layers/far_layers`: 控制挂单深度
- `base_layer_size`: 控制单次挂单量

### Q: 系统报错 "signature verification failed"

原因：API密钥或Secret错误

解决：
1. 检查API密钥是否正确
2. 确认API权限包含"Futures Trading"
3. 检查系统时间是否同步

### Q: 如何调整日志级别？

启动时使用 `-log` 参数：
```bash
./bin/phoenix -log debug  # 详细调试信息
./bin/phoenix -log info   # 正常运行信息（推荐）
./bin/phoenix -log warn   # 仅警告和错误
./bin/phoenix -log error  # 仅错误信息
```

### Q: 如何备份和恢复状态？

系统会自动保存快照到 `data/snapshot.json`

恢复：直接启动系统，会自动从快照恢复

手动备份：
```bash
cp data/snapshot.json data/snapshot_backup_$(date +%Y%m%d_%H%M%S).json
```

## 安全建议

1. **API密钥安全**
   - 生产环境使用环境变量，不要硬编码
   - 定期轮换API密钥
   - 设置IP白名单

2. **风控参数**
   - 从小仓位开始测试
   - 设置合理的 `net_max`
   - 监控 `stop_loss_thresh`

3. **监控告警**
   - 配置Prometheus告警
   - 监控仓位、PnL、订单拒绝率
   - 设置紧急停止机制

4. **测试流程**
   - 先在测试网充分测试
   - 小规模生产验证
   - 逐步扩大规模

## 下一步

- 阅读 `EXCHANGE_API_INTEGRATION.md` 了解API详情
- 阅读 `Phoenix高频做市商系统v2.md` 了解策略设计
- 查看 `PROJECT_STATUS.md` 了解项目状态
- 配置Grafana监控面板

## 技术支持

遇到问题请查看：
1. 系统日志输出
2. `PROJECT_STATUS.md` - 已知问题
3. `EXCHANGE_API_INTEGRATION.md` - API文档

---

**⚠️ 风险提示**

- 加密货币交易具有高风险
- 请在充分理解系统运作后使用
- 建议从测试网和小资金开始
- 做好风险管理和止损设置
