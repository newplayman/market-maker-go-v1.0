# Exchange API集成文档

## 概述

Phoenix v2已成功集成原项目(market-maker-go dev分支)的Binance API接口代码。

## 集成内容

### 核心文件
从原项目`gateway/`目录复制的核心API文件：

#### REST API
- `binance_rest.go` - REST接口定义
- `binance_rest_client.go` - REST客户端实现（28KB，完整实现）
- `binance_rest_client_test.go` - REST测试
- `rest.go` - 通用REST接口
- `rest_middleware.go` - 中间件（重试、限频）
- `rest_retry.go` - 重试逻辑

#### WebSocket
- `binance_ws.go` - WebSocket接口定义
- `binance_ws_real.go` - WebSocket实现
- `binance_ws_parser.go` - 消息解析器
- `binance_ws_parser_test.go` - 解析器测试
- `ws.go` - 通用WebSocket接口
- `ws_reconnect.go` - 断线重连逻辑
- `ws_test.go` - WebSocket测试

#### 认证与安全
- `binance_signature.go` - API签名
- `binance_signature_test.go` - 签名测试
- `binance_listenkey.go` - UserStream ListenKey管理
- `binance_listenkey_test.go` - ListenKey测试

#### 工具
- `binance.go` - Binance客户端封装
- `binance_env.go` - 环境变量配置
- `binance_factory.go` - 工厂方法
- `binance_user_handler.go` - 用户数据处理
- `binance_user_handler_test.go` - 用户数据测试
- `limiter.go` - 限频器

#### 适配层
- `adapter.go` - Exchange接口适配器（Phoenix v2）
- `types.go` - 数据类型定义（Phoenix v2）

### 测试结果

```bash
=== Exchange API测试 ===
✅ TestListenKeyLifecycle
✅ TestBinanceRESTClientPlaceCancel  
✅ TestBinanceRESTClientAccountBalances
✅ TestBinanceRESTClientPositionRisk
✅ TestBinanceRESTClientAccountInfo
✅ TestBinanceRESTClientLeverageBrackets
✅ TestBinanceRESTClientExchangeInfo
✅ TestBinanceRESTClientGetBestBidAsk
✅ TestBinanceRESTClientGetBestBidAskError
✅ TestBinanceRESTStubPlaceCancel
✅ TestSignParamsDeterministic
✅ TestBinanceUserHandler
✅ TestParseCombinedDepth
✅ TestParseUserOrderUpdate
✅ TestParseUserAccountUpdate
✅ TestClientPlaceCancel
✅ TestWSClient

总计：17个测试全部通过
```

## API功能

### REST API功能
- ✅ 下单/撤单 (PlaceLimit, CancelOrder)
- ✅ 账户信息查询 (AccountInfo, AccountBalances)
- ✅ 持仓信息 (PositionRisk)
- ✅ 杠杆档位 (LeverageBrackets)
- ✅ 交易所信息 (ExchangeInfo)
- ✅ 最优买卖价 (GetBestBidAsk)
- ✅ API签名认证
- ✅ 限频控制
- ✅ 自动重试

### WebSocket功能
- ✅ 深度流订阅 (Depth Stream)
- ✅ 用户数据流 (UserStream)
- ✅ 订单更新推送
- ✅ 仓位更新推送
- ✅ 资金费率推送
- ✅ 自动断线重连
- ✅ ListenKey自动续期

## 配置要求

### 环境变量
```bash
# Binance API
export BINANCE_API_KEY="your_api_key"
export BINANCE_SECRET_KEY="your_secret_key"

# 测试网（可选）
export BINANCE_TESTNET=true
export BINANCE_TESTNET_BASE_URL="https://testnet.binancefuture.com"
```

### 配置文件 (config.yaml)
```yaml
exchange:
  name: "binance"
  api_key: "${BINANCE_API_KEY}"
  secret_key: "${BINANCE_SECRET_KEY}"
  testnet: false
  
  # REST配置
  rest:
    timeout: 10s
    max_retries: 3
    
  # WebSocket配置
  websocket:
    reconnect_interval: 3s
    max_reconnects: 5
    ping_interval: 30s
```

## 使用示例

### 创建客户端
```go
import "github.com/newplayman/market-maker-phoenix/internal/exchange"

// 创建REST客户端
restClient := gateway.NewBinanceRESTClient(
    apiKey,
    secretKey,
    baseURL,
)

// 创建WebSocket客户端  
wsClient := gateway.NewBinanceWSReal(baseURL)

// 创建适配器
adapter := gateway.NewBinanceAdapter(restClient, wsClient)
```

### 下单
```go
order := &gateway.Order{
    Symbol:   "BTCUSDT",
    Side:     "BUY",
    Price:    50000.0,
    Quantity: 0.001,
}

placedOrder, err := adapter.PlaceOrder(ctx, order)
```

### 订阅深度流
```go
symbols := []string{"BTCUSDT", "ETHUSDT"}

err := adapter.StartDepthStream(ctx, symbols, func(depth *gateway.Depth) {
    log.Printf("深度更新: %s bid=%.2f ask=%.2f", 
        depth.Symbol, depth.Bids[0].Price, depth.Asks[0].Price)
})
```

## 架构集成

### 集成点
1. **Runner** → 使用 `Exchange` 接口
2. **Store** → 接收深度/持仓更新
3. **Risk** → 查询仓位/资金费率
4. **Strategy** → 下单/撤单

### 数据流
```
Binance API (REST/WS)
    ↓
BinanceAdapter (适配层)
    ↓
Runner (协调器)
    ↓
Strategy/Risk/Store (业务层)
```

## 已删除的文件

以下文件因依赖原项目其他模块而被删除：
- `binance_handler.go` - 依赖market模块
- `binance_handler_test.go` - 测试文件
- `binance_ws_handler.go` - 依赖market模块

这些功能已通过`adapter.go`重新实现。

## 下一步

- [ ] 配置真实API密钥进行测试网测试
- [ ] 实现完整的错误处理和日志
- [ ] 添加监控指标（API调用次数、延迟等）
- [ ] 压力测试（高频下单场景）
- [ ] 生产环境配置优化

## 版本信息

- **原项目**: market-maker-go (dev分支)
- **集成日期**: 2025-11-28
- **API版本**: Binance Futures API v1
- **测试状态**: ✅ 17/17 通过

## 注意事项

1. **限频管理**: API已内置限频控制，但高频场景需监控
2. **WebSocket稳定性**: 已实现自动重连，但需监控连接状态
3. **ListenKey**: 自动续期，无需手动管理
4. **签名安全**: API密钥应通过环境变量或密钥管理系统注入
5. **测试网**: 生产前务必在测试网充分测试

## 参考资料

- [Binance Futures API文档](https://binance-docs.github.io/apidocs/futures/cn/)
- [原项目地址](https://github.com/newplayman/market-maker-go)
- Phoenix v2设计文档: `Phoenix高频做市商系统v2.md`
