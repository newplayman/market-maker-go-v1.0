# Phoenix v2 高频做市商系统 - 项目总结

## 项目概述

Phoenix v2 是一个基于 Go 语言开发的高频做市商系统，专为币安 USDC-M 永续合约设计。系统采用模块化架构，实现了自适应做市（ASMM）策略，包含钉子模式和磨仓模式。

## 项目结构

```
Phoenix v2/
├── cmd/runner/              # 主程序入口
│   └── main.go             # 启动逻辑
├── internal/
│   ├── config/             # 配置管理
│   │   └── config.go       # 配置加载与热重载
│   ├── exchange/           # 交易所接口层
│   │   ├── types.go        # 数据类型定义
│   │   ├── adapter.go      # Exchange适配器
│   │   ├── binance.go      # Binance客户端接口
│   │   ├── binance_rest.go # REST API stub
│   │   └── binance_ws.go   # WebSocket stub
│   ├── strategy/           # 策略模块
│   │   ├── strategy.go     # 策略接口与ASMM实现
│   │   ├── grinding.go     # 磨仓模式实现
│   │   └── errors.go       # 错误定义
│   ├── risk/               # 风控模块
│   │   ├── risk.go         # 风控管理器
│   │   └── grinding.go     # 磨仓风控
│   ├── store/              # 状态存储
│   │   └── store.go        # 全局状态管理
│   ├── metrics/            # 监控指标
│   │   └── metrics.go      # Prometheus指标
│   └── runner/             # 核心运行器
│       └── runner.go       # 主循环逻辑
├── config.yaml.example     # 配置示例
├── Dockerfile              # Docker构建文件
├── Makefile               # 构建脚本
├── go.mod                 # Go模块定义
└── README.md              # 项目说明

编译产物:
├── bin/phoenix            # 可执行文件 (13MB)
```

## 核心模块说明

### 1. Config 模块 (`internal/config/`)
- 支持 YAML 配置文件
- 热重载功能
- 环境变量覆盖
- 配置验证

### 2. Exchange 模块 (`internal/exchange/`)
- 统一的 Exchange 接口
- Binance 适配器实现
- REST API 和 WebSocket 支持
- Stub 实现用于测试

### 3. Strategy 模块 (`internal/strategy/`)
- ASMM（自适应做市）策略
- 库存偏移调整
- 波动率自适应
- 钉子模式（Pinning）
- 磨仓模式（Grinding）

### 4. Risk 模块 (`internal/risk/`)
- 交易前风控检查
- 仓位限制管理
- 止损检查
- 报价验证
- 磨仓风控

### 5. Store 模块 (`internal/store/`)
- 全局状态管理
- 仓位跟踪
- 价格历史
- 快照持久化

### 6. Metrics 模块 (`internal/metrics/`)
- Prometheus 指标导出
- 仓位指标
- 交易指标
- 风控指标
- 系统性能指标

### 7. Runner 模块 (`internal/runner/`)
- 主事件循环
- 多交易对并发处理
- WebSocket 事件处理
- 订单管理

## 编译与运行

### 编译
```bash
make build
# 或
go build -o bin/phoenix ./cmd/runner
```

### 运行
```bash
./bin/phoenix -config config.yaml -log info
```

### Docker
```bash
docker build -t phoenix-v2 .
docker run -v $(pwd)/config.yaml:/app/config.yaml phoenix-v2
```

## 配置说明

参考 `config.yaml.example` 文件，主要配置项：

- `global`: 全局配置（API密钥、总仓位限制等）
- `symbols`: 交易对配置（价差、层级、风控参数等）

## 监控

系统在配置的端口（默认 9090）暴露 Prometheus 指标：

```
http://localhost:9090/metrics
```

## 主要特性

1. **自适应做市**: 根据库存和波动率动态调整报价
2. **钉子模式**: 仓位过大时单边报价
3. **磨仓模式**: 主动平仓降低风险
4. **多层报价**: 近端密集 + 远端稀疏
5. **风控保护**: 多重风控检查
6. **实时监控**: Prometheus 指标
7. **配置热重载**: 无需重启即可更新配置
8. **状态持久化**: 定期保存快照

## 技术栈

- **语言**: Go 1.21+
- **日志**: zerolog
- **配置**: viper
- **监控**: Prometheus
- **并发**: goroutines + channels

## 开发状态

✅ 已完成：
- 核心架构搭建
- 所有模块实现
- ASMM 策略
- 磨仓模式
- 风控系统
- 监控指标
- 编译通过

🔄 待完善：
- 真实 Binance API 集成（当前使用 stub）
- 单元测试
- 集成测试
- 性能优化
- 文档完善

## 注意事项

1. 当前使用 stub 实现，不会真实下单
2. 需要配置有效的 Binance API 密钥
3. 建议先在测试网测试
4. 注意风控参数设置
5. 监控系统运行状态

## 下一步计划

1. 集成真实 Binance API
2. 添加单元测试和集成测试
3. 性能测试和优化
4. 完善错误处理
5. 添加更多策略模式
6. 改进日志和监控

---

**构建时间**: 2025-11-28  
**版本**: v2.0  
**状态**: 编译成功，核心功能完整
