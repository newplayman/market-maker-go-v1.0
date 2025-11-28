# 币安 WebSocket 交易 API 研究报告

## 问题背景

在尝试使用币安 Futures WebSocket API 进行下单和撤单操作时，遇到了以下错误序列：

1. **错误 -5000**: `Method v1/login is invalid`
2. **错误 -1022**: `Signature for this request is not valid`
3. **错误 -4056**: `HMAC_SHA256 API key is not supported`

## 问题分析

### 1. 方法名错误
- **错误代码**: -5000
- **原因**: 使用了错误的登录方法 `v1/login`
- **正确方法**: `session.logon`
- **修复**: 移除 v1/v2 前缀，直接使用方法名

### 2. 签名问题
- **错误代码**: -1022
- **原因**: 签名时未包含 `apiKey` 参数
- **修复**: 签名字符串改为 `apiKey={key}&timestamp={ts}`

### 3. API Key 权限问题（核心问题）
- **错误代码**: -4056
- **原因**: 当前 API Key 不支持 HMAC_SHA256 签名的 WebSocket 交易
- **可能原因**:
  1. API Key 未开启 WebSocket 交易权限
  2. 币安可能要求使用 **ED25519** 签名而非 HMAC_SHA256
  3. 某些账户类型可能不支持 WebSocket 交易 API

## 币安 WebSocket 交易 API 要求

根据币安文档，WebSocket 交易 API 可能需要：

### 1. API Key 配置要求
- 需要在币安账户中创建 API Key 时勾选 "Enable Spot & Margin Trading"
- 可能需要额外开启 "WebSocket API" 权限
- 部分功能可能需要 KYC 验证

### 2. 签名方式
币安支持两种签名方式：
- **HMAC_SHA256**: 传统签名方式
- **ED25519**: 更安全的签名方式（可能是 WebSocket API 的要求）

### 3. 连接端点
- **测试网**: `wss://testnet.binancefuture.com/ws-fapi/v1`
- **主网**: `wss://ws-fapi.binance.com/ws-fapi/v1`

## 解决方案

### 方案1: 检查 API Key 权限（推荐）
1. 登录币安账户
2. 进入 API 管理页面
3. 检查当前 API Key 是否开启了以下权限：
   - ✅ Enable Spot & Margin Trading
   - ✅ Enable Futures
   - ✅ Enable WebSocket API (如果有此选项)
4. 如果没有相关权限，创建新的 API Key 并开启所有交易权限

### 方案2: 使用 ED25519 签名
如果 HMAC_SHA256 不被支持，需要：
1. 生成 ED25519 密钥对
2. 在币安账户中上传公钥
3. 修改代码使用 ED25519 签名算法

### 方案3: 继续使用 REST API（当前方案）
优点：
- ✅ 立即可用，无需额外配置
- ✅ 稳定可靠
- ✅ 支持所有币安功能

缺点：
- ❌ 延迟略高（20-50ms vs 10-20ms）
- ❌ 需要处理 HTTP 重试和错误

## 当前实现状态

### 已完成
- ✅ WebSocket 行情数据接收（深度流、成交流）
- ✅ WebSocket 用户数据流（订单更新、持仓更新）
- ✅ REST API 下单和撤单（作为 fallback）
- ✅ Post Only 订单支持（Maker 免手续费）

### 待完成（WSS 交易）
- ⏸️ 解决 API Key 权限问题
- ⏸️ 实现 ED25519 签名（如需要）
- ⏸️ WebSocket 下单和撤单

## 性能对比

| 功能 | REST API | WebSocket API |
|------|----------|---------------|
| 下单延迟 | 20-50ms | 10-20ms |
| 撤单延迟 | 20-50ms | 10-20ms |
| 可靠性 | 高（支持重试） | 中（需要心跳） |
| 实现复杂度 | 低 | 中 |
| 权限要求 | 基础交易权限 | 特殊 WebSocket 权限 |

## 建议

### 短期（当前实盘测试）
**继续使用 REST API**，原因：
1. 立即可用，已验证工作正常
2. 延迟差异对做市商策略影响不大（报价间隔 1000ms）
3. 稳定性更高

### 长期（优化）
1. **联系币安客服** 确认 WebSocket 交易 API 的具体要求
2. **检查账户权限** 确保 API Key 具备所有必要权限
3. **测试 ED25519 签名** 如果 HMAC_SHA256 确实不被支持
4. **性能测试** 对比 REST 和 WSS 在实际场景下的表现差异

## 技术细节

### REST API 实现
```go
// 当前使用的方式
orderID, err := b.rest.PlaceLimit(
    symbol, side, "GTC", 
    price, qty, 
    false, true, // reduceOnly, postOnly
    clientOrderID,
)
```

### WebSocket API 实现（待修复）
```go
// WSS 方式（需要修复权限问题）
params := TradeOrderParams{
    Symbol:        symbol,
    Side:          side,
    Type:          "LIMIT",
    Quantity:      qty,
    Price:         price,
    TimeInForce:   "GTC",
    ClientOrderID: clientOrderID,
    PostOnly:      true,
}
result, err := b.tradeWS.PlaceOrder(ctx, params)
```

## 参考资料

- [币安 Futures WebSocket API 文档](https://binance-docs.github.io/apidocs/futures/en/#websocket-api)
- [币安 API 权限说明](https://www.binance.com/en/support/faq/how-to-create-api-360002502072)
- [ED25519 签名文档](https://binance-docs.github.io/apidocs/spot/en/#ed25519)

---
**报告日期**: 2025-11-28  
**状态**: REST API 运行正常，WSS API 待权限解决
