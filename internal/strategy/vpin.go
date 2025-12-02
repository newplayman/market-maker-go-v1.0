package strategy

import (
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// VPINConfig VPIN计算器配置
type VPINConfig struct {
	BucketSize    float64 // 每个bucket的成交量大小（例如50000）
	NumBuckets    int     // 滚动窗口的bucket数量（例如50）
	Threshold     float64 // 警报阈值（例如0.7）
	PauseThresh   float64 // 暂停阈值（例如0.9）
	Multiplier    float64 // 价差放大系数（例如0.2）
	VolThreshold  float64 // 最小总成交量要求（例如100000）
}

// DefaultVPINConfig 返回默认VPIN配置
func DefaultVPINConfig() VPINConfig {
	return VPINConfig{
		BucketSize:   50000.0, // 50k shares/bucket
		NumBuckets:   50,      // 50个buckets
		Threshold:    0.7,     // 0.7阈值触发spread放大
		PauseThresh:  0.9,     // 0.9阈值触发暂停
		Multiplier:   0.2,     // spread放大20%
		VolThreshold: 100000.0, // 最小100k总成交量
	}
}

// VolumeBucket 表示一个成交量桶
type VolumeBucket struct {
	BuyVolume  float64   // 买方成交量
	SellVolume float64   // 卖方成交量
	Timestamp  time.Time // 桶创建时间
}

// TotalVolume 返回桶的总成交量
func (b *VolumeBucket) TotalVolume() float64 {
	return b.BuyVolume + b.SellVolume
}

// Imbalance 返回买卖不平衡度
func (b *VolumeBucket) Imbalance() float64 {
	return math.Abs(b.BuyVolume - b.SellVolume)
}

// Trade 表示一笔交易
type Trade struct {
	Symbol    string
	Price     float64
	Quantity  float64
	Timestamp time.Time
	IsBuy     bool // Lee-Ready分类结果
}

// VPINCalculator VPIN计算器
type VPINCalculator struct {
	mu sync.RWMutex

	config  VPINConfig
	symbol  string
	buckets []VolumeBucket // 固定大小的环形缓冲区

	currentBucket  VolumeBucket // 当前正在填充的bucket
	bucketIndex    int          // 当前bucket在环形缓冲区中的位置
	filledBuckets  int          // 已填充的bucket数量
	lastMidPrice   float64      // 最后一次的中间价
	totalTrades    int64        // 总交易数
	lastUpdateTime time.Time    // 最后更新时间
}

// NewVPINCalculator 创建新的VPIN计算器
func NewVPINCalculator(symbol string, cfg VPINConfig) *VPINCalculator {
	if cfg.NumBuckets <= 0 {
		cfg.NumBuckets = 50
	}
	if cfg.BucketSize <= 0 {
		cfg.BucketSize = 50000
	}

	return &VPINCalculator{
		config:         cfg,
		symbol:         symbol,
		buckets:        make([]VolumeBucket, cfg.NumBuckets),
		currentBucket:  VolumeBucket{Timestamp: time.Now()},
		bucketIndex:    0,
		filledBuckets:  0,
		lastUpdateTime: time.Now(),
	}
}

// UpdateMidPrice 更新中间价（用于Lee-Ready分类）
func (v *VPINCalculator) UpdateMidPrice(mid float64) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if mid > 0 {
		v.lastMidPrice = mid
	}
}

// UpdateTrade 更新交易数据（核心方法）
// 使用Lee-Ready算法分类交易方向
func (v *VPINCalculator) UpdateTrade(trade Trade) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if trade.Quantity <= 0 {
		return ErrInvalidTradeQuantity
	}

	// Lee-Ready算法分类买卖方向
	// 如果trade已经标记了IsBuy，使用标记；否则根据价格分类
	isBuy := trade.IsBuy
	if v.lastMidPrice > 0 && !trade.IsBuy && trade.Price > 0 {
		// 价格 >= mid → 买方发起
		// 价格 < mid → 卖方发起
		isBuy = trade.Price >= v.lastMidPrice
	}

	// 更新当前bucket
	if isBuy {
		v.currentBucket.BuyVolume += trade.Quantity
	} else {
		v.currentBucket.SellVolume += trade.Quantity
	}

	v.totalTrades++
	v.lastUpdateTime = time.Now()

	// 检查当前bucket是否已满
	if v.currentBucket.TotalVolume() >= v.config.BucketSize {
		// 保存当前bucket到环形缓冲区
		v.buckets[v.bucketIndex] = v.currentBucket

		// 移动到下一个bucket
		v.bucketIndex = (v.bucketIndex + 1) % v.config.NumBuckets
		if v.filledBuckets < v.config.NumBuckets {
			v.filledBuckets++
		}

		// 开始新的bucket
		v.currentBucket = VolumeBucket{
			Timestamp: time.Now(),
		}

		log.Debug().
			Str("symbol", v.symbol).
			Int("filled_buckets", v.filledBuckets).
			Float64("vpin", v.calculateVPINLocked()).
			Msg("VPIN bucket已满，计算新VPIN")
	}

	return nil
}

// GetVPIN 获取当前VPIN值（0-1之间）
// 返回值越高表示订单流毒性越大
func (v *VPINCalculator) GetVPIN() float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.calculateVPINLocked()
}

// calculateVPINLocked 计算VPIN（调用者必须持有锁）
func (v *VPINCalculator) calculateVPINLocked() float64 {
	// 数据不足时返回中性值0.5
	minBuckets := 5
	if v.filledBuckets < minBuckets {
		return 0.5
	}

	// 计算所有buckets的累计买卖量和总量
	var totalBuy, totalSell, totalVolume float64

	// 遍历已填充的buckets
	numBucketsToUse := v.filledBuckets
	if numBucketsToUse > v.config.NumBuckets {
		numBucketsToUse = v.config.NumBuckets
	}

	for i := 0; i < numBucketsToUse; i++ {
		bucket := v.buckets[i]
		totalBuy += bucket.BuyVolume
		totalSell += bucket.SellVolume
		totalVolume += bucket.TotalVolume()
	}

	// 检查总成交量是否满足最小要求
	if totalVolume < v.config.VolThreshold {
		return 0.5 // 成交量不足，返回中性值
	}

	// VPIN = |买量 - 卖量| / 总量
	// 公式来自Easley et al. (2012)
	imbalance := math.Abs(totalBuy - totalSell)
	vpin := imbalance / totalVolume

	// 确保VPIN在[0, 1]范围内
	if vpin < 0 {
		vpin = 0
	}
	if vpin > 1 {
		vpin = 1
	}

	return vpin
}

// GetStats 获取VPIN计算器统计信息
func (v *VPINCalculator) GetStats() VPINStats {
	v.mu.RLock()
	defer v.mu.RUnlock()

	vpin := v.calculateVPINLocked()

	return VPINStats{
		Symbol:         v.symbol,
		VPIN:           vpin,
		FilledBuckets:  v.filledBuckets,
		TotalBuckets:   v.config.NumBuckets,
		TotalTrades:    v.totalTrades,
		LastUpdateTime: v.lastUpdateTime,
		CurrentBucket:  v.currentBucket,
		IsWarning:      vpin >= v.config.Threshold,
		ShouldPause:    vpin >= v.config.PauseThresh,
	}
}

// VPINStats VPIN统计信息
type VPINStats struct {
	Symbol         string
	VPIN           float64
	FilledBuckets  int
	TotalBuckets   int
	TotalTrades    int64
	LastUpdateTime time.Time
	CurrentBucket  VolumeBucket
	IsWarning      bool // VPIN >= threshold
	ShouldPause    bool // VPIN >= pause_threshold
}

// Reset 重置VPIN计算器
func (v *VPINCalculator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.buckets = make([]VolumeBucket, v.config.NumBuckets)
	v.currentBucket = VolumeBucket{Timestamp: time.Now()}
	v.bucketIndex = 0
	v.filledBuckets = 0
	v.totalTrades = 0
	v.lastUpdateTime = time.Now()

	log.Info().
		Str("symbol", v.symbol).
		Msg("VPIN计算器已重置")
}

// GetConfig 获取配置
func (v *VPINCalculator) GetConfig() VPINConfig {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.config
}

// UpdateConfig 更新配置（热重载支持）
func (v *VPINCalculator) UpdateConfig(cfg VPINConfig) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 只更新阈值参数，不改变bucket结构避免数据丢失
	v.config.Threshold = cfg.Threshold
	v.config.PauseThresh = cfg.PauseThresh
	v.config.Multiplier = cfg.Multiplier

	log.Info().
		Str("symbol", v.symbol).
		Float64("threshold", cfg.Threshold).
		Float64("pause_thresh", cfg.PauseThresh).
		Float64("multiplier", cfg.Multiplier).
		Msg("VPIN配置已更新")
}

