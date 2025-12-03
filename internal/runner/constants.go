package runner

// 【P1-2】系统常量定义 - 提取魔法数字,提高可维护性

const (
	// WebSocket相关常量
	STALE_PRICE_THRESHOLD_SECONDS = 5  // 价格数据过期阈值(秒)
	WS_RECONNECT_DELAY_SECONDS    = 2  // WebSocket重连延迟(秒)
	WS_RECONNECT_MAX_BACKOFF      = 64 // 最大退避时间(秒)

	// Channel缓冲相关 - 【紧急修复】增大缓冲
	DEPTH_CHANNEL_BUFFER_SIZE = 500 // 深度消息channel缓冲大小(从100增加到500)
	DEPTH_CHANNEL_WARNING_PCT = 0.8 // channel使用率警告阈值(80%)
	DEPTH_DROP_LOG_INTERVAL   = 100 // 每N条丢弃记录一次日志

	// 订单管理相关
	ORDER_OVERFLOW_THRESHOLD = 50   // 订单溢出熔断阈值
	CANCEL_RATE_WARNING_PCT  = 0.95 // 撤单频率警告阈值(95%)

	// 性能监控相关
	DEPTH_PROCESSING_SLOW_MS = 100 // 深度处理慢速阈值(毫秒)

	// 防闪烁相关
	TOLERANCE_FACTOR          = 0.9 // 防闪烁容差系数(90%)
	MIN_TOLERANCE_TICKS       = 10  // 最小容差(tick数)
	MAX_TOLERANCE_SPREAD_MULT = 3.0 // 最大容差(MinSpread的倍数)

	// 风控相关
	MAX_WORST_CASE_RATIO        = 0.5 // 最坏情况敞口比例(50% NetMax)
	RISK_ADJUSTMENT_RATIO       = 0.9 // 风控调整比例(每次减少10%)
	MAX_RISK_ADJUSTMENT_RETRIES = 5   // 最大风控调整重试次数
)
