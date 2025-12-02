package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

var (
	// 仓位指标
	PositionSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_position_size",
			Help: "当前仓位大小",
		},
		[]string{"symbol"},
	)

	PositionNotional = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_position_notional",
			Help: "仓位名义价值",
		},
		[]string{"symbol"},
	)

	UnrealizedPNL = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_unrealized_pnl",
			Help: "未实现盈亏",
		},
		[]string{"symbol"},
	)

	// 挂单指标
	PendingBuySize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_pending_buy_size",
			Help: "挂买单总量",
		},
		[]string{"symbol"},
	)

	PendingSellSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_pending_sell_size",
			Help: "挂卖单总量",
		},
		[]string{"symbol"},
	)

	// 市场数据指标
	MidPrice = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_mid_price",
			Help: "中间价",
		},
		[]string{"symbol"},
	)

	PriceSpread = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_price_spread",
			Help: "买卖价差比例",
		},
		[]string{"symbol"},
	)

	FundingRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_funding_rate",
			Help: "资金费率",
		},
		[]string{"symbol"},
	)

	// 交易指标
	FillCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_fill_count_total",
			Help: "成交次数",
		},
		[]string{"symbol", "side"},
	)

	FillVolume = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_fill_volume_total",
			Help: "成交量",
		},
		[]string{"symbol", "side"},
	)

	TotalPNL = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_total_pnl",
			Help: "累计盈亏",
		},
		[]string{"symbol"},
	)

	// 风控指标
	WorstCaseLong = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_worst_case_long",
			Help: "最坏情况多头敞口",
		},
		[]string{"symbol"},
	)

	TotalNotional = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "phoenix_total_notional",
			Help: "总名义价值",
		},
	)

	MaxDrawdown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_max_drawdown",
			Help: "最大回撤",
		},
		[]string{"symbol"},
	)

	CancelRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_cancel_rate",
			Help: "每分钟撤单数",
		},
		[]string{"symbol"},
	)

	// 系统指标
	QuoteGeneration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "phoenix_quote_generation_duration_seconds",
			Help:    "报价生成耗时",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"symbol"},
	)

	OrderPlacement = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "phoenix_order_placement_duration_seconds",
			Help:    "下单耗时",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"symbol", "side"},
	)

	APILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "phoenix_api_latency_seconds",
			Help:    "API请求延迟",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		},
		[]string{"endpoint", "status"},
	)

	ErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_error_count_total",
			Help: "错误计数",
		},
		[]string{"type", "symbol"},
	)

	// 策略指标
	StrategyMode = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_strategy_mode",
			Help: "策略模式 (0=正常, 1=钉子, 2=磨仓)",
		},
		[]string{"symbol"},
	)

	InventorySkew = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_inventory_skew",
			Help: "库存偏移",
		},
		[]string{"symbol"},
	)

	VolatilityScaling = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_volatility_scaling",
			Help: "波动率调整系数",
		},
		[]string{"symbol"},
	)

	// VPIN指标
	VPINCurrent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_vpin_current",
			Help: "当前VPIN值（0-1，越高表示订单流毒性越大）",
		},
		[]string{"symbol"},
	)

	VPINBucketCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_vpin_bucket_count",
			Help: "已填充的VPIN buckets数量",
		},
		[]string{"symbol"},
	)

	VPINPauseCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_vpin_pause_total",
			Help: "因VPIN过高而暂停报价的次数",
		},
		[]string{"symbol"},
	)

	VPINSpreadMultiplier = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_vpin_spread_multiplier",
			Help: "VPIN引起的价差放大倍数",
		},
		[]string{"symbol"},
	)

	// WebSocket流量监控（专家建议：防止数据雪球）
	WSBytesReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_ws_bytes_received_total",
			Help: "WebSocket接收字节数（下行流量）",
		},
		[]string{"symbol"},
	)

	WSMessageCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phoenix_ws_message_count_total",
			Help: "WebSocket消息数量（按类型统计）",
		},
		[]string{"symbol", "type"}, // type: depth, trade, order, account
	)

	WSBandwidthRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "phoenix_ws_bandwidth_bytes_per_min",
			Help: "WebSocket带宽速率（字节/分钟）",
		},
		[]string{"symbol"},
	)
)

func init() {
	// 注册所有指标
	prometheus.MustRegister(
		PositionSize,
		PositionNotional,
		UnrealizedPNL,
		PendingBuySize,
		PendingSellSize,
		MidPrice,
		PriceSpread,
		FundingRate,
		FillCount,
		FillVolume,
		TotalPNL,
		WorstCaseLong,
		TotalNotional,
		MaxDrawdown,
		CancelRate,
		QuoteGeneration,
		OrderPlacement,
		APILatency,
		ErrorCount,
		StrategyMode,
		InventorySkew,
		VolatilityScaling,
		VPINCurrent,
		VPINBucketCount,
		VPINPauseCount,
		VPINSpreadMultiplier,
		WSBytesReceived,
		WSMessageCount,
		WSBandwidthRate,
	)
}

// StartMetricsServer 启动Prometheus监控服务器
func StartMetricsServer(port int) error {
	http.Handle("/metrics", promhttp.Handler())
	addr := fmt.Sprintf(":%d", port)

	log.Info().Int("port", port).Msg("启动Prometheus监控服务器")

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Error().Err(err).Msg("Prometheus服务器启动失败")
		}
	}()

	return nil
}

// RecordFill 记录成交
func RecordFill(symbol, side string, size float64) {
	FillCount.WithLabelValues(symbol, side).Inc()
	FillVolume.WithLabelValues(symbol, side).Add(size)
}

// RecordError 记录错误
func RecordError(errType, symbol string) {
	ErrorCount.WithLabelValues(errType, symbol).Inc()
}

// UpdatePositionMetrics 更新仓位指标
func UpdatePositionMetrics(symbol string, size, notional, unrealizedPNL float64) {
	PositionSize.WithLabelValues(symbol).Set(size)
	PositionNotional.WithLabelValues(symbol).Set(notional)
	UnrealizedPNL.WithLabelValues(symbol).Set(unrealizedPNL)
}

// UpdatePendingMetrics 更新挂单指标
func UpdatePendingMetrics(symbol string, buySize, sellSize float64) {
	PendingBuySize.WithLabelValues(symbol).Set(buySize)
	PendingSellSize.WithLabelValues(symbol).Set(sellSize)
}

// UpdateMarketMetrics 更新市场数据指标
func UpdateMarketMetrics(symbol string, mid, spread, funding float64) {
	MidPrice.WithLabelValues(symbol).Set(mid)
	PriceSpread.WithLabelValues(symbol).Set(spread)
	FundingRate.WithLabelValues(symbol).Set(funding)
}

// UpdateVPINMetrics 更新VPIN指标
func UpdateVPINMetrics(symbol string, vpin float64, bucketCount int, spreadMultiplier float64) {
	VPINCurrent.WithLabelValues(symbol).Set(vpin)
	VPINBucketCount.WithLabelValues(symbol).Set(float64(bucketCount))
	VPINSpreadMultiplier.WithLabelValues(symbol).Set(spreadMultiplier)
}

// IncrementVPINPauseCount 增加VPIN暂停计数
func IncrementVPINPauseCount(symbol string) {
	VPINPauseCount.WithLabelValues(symbol).Inc()
}

// RecordWSMessage 记录WebSocket消息
func RecordWSMessage(symbol, msgType string, bytes int) {
	WSBytesReceived.WithLabelValues(symbol).Add(float64(bytes))
	WSMessageCount.WithLabelValues(symbol, msgType).Inc()
}

// UpdateWSBandwidthRate 更新WebSocket带宽速率（每分钟调用）
func UpdateWSBandwidthRate(symbol string, bytesPerMin float64) {
	WSBandwidthRate.WithLabelValues(symbol).Set(bytesPerMin)
}
