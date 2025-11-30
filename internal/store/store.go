package store

import (
	"encoding/json"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// Position 仓位信息
type Position struct {
	Symbol         string    `json:"symbol"`
	Size           float64   `json:"size"`           // 仓位大小（正为多，负为空）
	EntryPrice     float64   `json:"entry_price"`    // 开仓均价
	UnrealizedPNL  float64   `json:"unrealized_pnl"` // 未实现盈亏
	Leverage       float64   `json:"leverage"`       // 杠杆倍数
	Notional       float64   `json:"notional"`       // 名义价值
	LastUpdateTime time.Time `json:"last_update_time"`
}

// SymbolState 单个交易对的状态
type SymbolState struct {
	Mu sync.RWMutex

	Symbol      string
	Position    Position
	PendingBuy  float64   // 挂单买入量
	PendingSell float64   // 挂单卖出量
	MidPrice    float64   // 中间价
	BestBid     float64   // 最优买价
	BestAsk     float64   // 最优卖价
	FundingRate float64   // 资金费率
	LastFill    time.Time // 最后成交时间

	// 活跃订单数量统计
	ActiveOrderCount int // 实际活跃订单数量

	// 价格历史（环形缓冲）
	PriceHistory      []float64
	PriceHistoryIndex int
	PriceHistorySize  int

	// 资金费率历史（用于EMA计算）
	FundingHistory []float64

	// 统计信息
	FillCount       int64   // 成交次数
	TotalVolume     float64 // 总成交量
	TotalPNL        float64 // 累计盈亏
	MaxDrawdown     float64 // 最大回撤
	CancelCountLast int     // 最近一分钟撤单数
	LastCancelReset time.Time

	// 策略状态
	LastMode string // 最后使用的策略模式 (normal/pinning/grinding)
}

type Store struct {
	mu              sync.RWMutex
	symbols         map[string]*SymbolState
	totalNotional   atomic.Value // float64
	snapshotPath    string
	snapshotTicker  *time.Ticker
	stopSnapshot    chan struct{}
	lastSnapshotErr error
}

// GetActiveOrderCount 获取指定符号当前活跃订单数量
func (s *Store) GetActiveOrderCount(symbol string) int {
	s.mu.RLock()
	state, exists := s.symbols[symbol]
	s.mu.RUnlock()

	if !exists || state == nil {
		return 0
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	// 返回实际统计的活跃订单数量
	return state.ActiveOrderCount
}

// SetActiveOrderCount 设置指定符号的活跃订单数量
func (s *Store) SetActiveOrderCount(symbol string, count int) {
	s.mu.RLock()
	state, exists := s.symbols[symbol]
	s.mu.RUnlock()

	if !exists || state == nil {
		return
	}

	state.Mu.Lock()
	state.ActiveOrderCount = count
	state.Mu.Unlock()
}

// NewStore 创建新的存储实例
func NewStore(snapshotPath string, snapshotInterval time.Duration) *Store {
	s := &Store{
		symbols:        make(map[string]*SymbolState),
		snapshotPath:   snapshotPath,
		snapshotTicker: time.NewTicker(snapshotInterval),
		stopSnapshot:   make(chan struct{}),
	}

	s.totalNotional.Store(float64(0))

	// 启动快照协程
	go s.runSnapshotLoop()

	// 尝试从快照恢复
	if err := s.LoadSnapshot(); err != nil {
		log.Warn().Err(err).Msg("无法从快照恢复，使用空状态")
	}

	return s
}

// InitSymbol 初始化交易对状态
func (s *Store) InitSymbol(symbol string, priceHistorySize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.symbols[symbol]; exists {
		return
	}

	s.symbols[symbol] = &SymbolState{
		Symbol:           symbol,
		PriceHistory:     make([]float64, priceHistorySize),
		PriceHistorySize: priceHistorySize,
		FundingHistory:   make([]float64, 0, 24), // 24小时
		LastCancelReset:  time.Now(),
	}

	log.Info().Str("symbol", symbol).Msg("交易对状态初始化完成")
}

// GetSymbolState 获取交易对状态（只读）
func (s *Store) GetSymbolState(symbol string) *SymbolState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.symbols[symbol]
}

// UpdatePosition 更新仓位
func (s *Store) UpdatePosition(symbol string, pos Position) {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		log.Warn().Str("symbol", symbol).Msg("交易对未初始化")
		return
	}

	state.Mu.Lock()
	state.Position = pos
	state.Mu.Unlock()

	// 更新全局名义价值
	s.updateTotalNotional()
}

// UpdateMidPrice 更新中间价
func (s *Store) UpdateMidPrice(symbol string, mid, bestBid, bestAsk float64) {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	state.MidPrice = mid
	state.BestBid = bestBid
	state.BestAsk = bestAsk

	// 添加到价格历史
	state.PriceHistory[state.PriceHistoryIndex] = mid
	state.PriceHistoryIndex = (state.PriceHistoryIndex + 1) % state.PriceHistorySize
}

// UpdateFundingRate 更新资金费率
func (s *Store) UpdateFundingRate(symbol string, rate float64) {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	state.FundingRate = rate
	state.FundingHistory = append(state.FundingHistory, rate)

	// 保持最近24个资金费率
	if len(state.FundingHistory) > 24 {
		state.FundingHistory = state.FundingHistory[1:]
	}
}

// UpdatePendingOrders 更新挂单量
func (s *Store) UpdatePendingOrders(symbol string, buy, sell float64) {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return
	}

	state.Mu.Lock()
	state.PendingBuy = buy
	state.PendingSell = sell
	state.Mu.Unlock()
}

// RecordFill 记录成交
func (s *Store) RecordFill(symbol string, size, pnl float64) {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	atomic.AddInt64(&state.FillCount, 1)
	state.TotalVolume += math.Abs(size)
	state.TotalPNL += pnl
	state.LastFill = time.Now()

	// 更新最大回撤
	if pnl < 0 && math.Abs(pnl) > state.MaxDrawdown {
		state.MaxDrawdown = math.Abs(pnl)
	}
}

// IncrementCancelCount 增加撤单计数
func (s *Store) IncrementCancelCount(symbol string) int {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	// 检查是否需要重置计数（每分钟）
	if time.Since(state.LastCancelReset) > time.Minute {
		state.CancelCountLast = 0
		state.LastCancelReset = time.Now()
	}

	state.CancelCountLast++
	return state.CancelCountLast
}

// GetWorstCaseLong 获取最坏情况多头敞口（仓位 + 挂买单 - 挂卖单）
func (s *Store) GetWorstCaseLong(symbol string) float64 {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	return state.Position.Size + state.PendingBuy - state.PendingSell
}

// PriceStdDev30m 计算30分钟价格标准差
func (s *Store) PriceStdDev30m(symbol string) float64 {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	// 计算均值
	var sum float64
	count := 0
	for _, price := range state.PriceHistory {
		if price > 0 {
			sum += price
			count++
		}
	}

	if count == 0 {
		return 0
	}

	mean := sum / float64(count)

	// 计算标准差
	var variance float64
	for _, price := range state.PriceHistory {
		if price > 0 {
			diff := price - mean
			variance += diff * diff
		}
	}

	return math.Sqrt(variance / float64(count))
}

// PredictedFunding 预测资金费率（EMA）
func (s *Store) PredictedFunding(symbol string) float64 {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	if len(state.FundingHistory) == 0 {
		return 0
	}

	// 简单EMA，alpha=0.3
	alpha := 0.3
	ema := state.FundingHistory[0]
	for i := 1; i < len(state.FundingHistory); i++ {
		ema = alpha*state.FundingHistory[i] + (1-alpha)*ema
	}

	return ema
}

// GetTotalNotional 获取总名义价值
func (s *Store) GetTotalNotional() float64 {
	return s.totalNotional.Load().(float64)
}

// IsOverCap 检查是否超过总名义价值上限
func (s *Store) IsOverCap(maxNotional float64) bool {
	return s.GetTotalNotional() > maxNotional
}

// updateTotalNotional 更新总名义价值
func (s *Store) updateTotalNotional() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.updateTotalNotionalLocked()
}

// updateTotalNotionalLocked 更新总名义价值（调用者必须持有锁）
func (s *Store) updateTotalNotionalLocked() {
	var total float64
	for _, state := range s.symbols {
		state.Mu.RLock()
		total += math.Abs(state.Position.Notional)
		state.Mu.RUnlock()
	}

	s.totalNotional.Store(total)
}

// SaveSnapshot 保存快照
func (s *Store) SaveSnapshot() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]*SymbolState)
	for symbol, state := range s.symbols {
		state.Mu.RLock()
		// 复制状态
		stateCopy := *state
		state.Mu.RUnlock()
		snapshot[symbol] = &stateCopy
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(s.snapshotPath, data, 0644); err != nil {
		return err
	}

	log.Debug().Str("path", s.snapshotPath).Msg("快照保存成功")
	return nil
}

// LoadSnapshot 加载快照
func (s *Store) LoadSnapshot() error {
	data, err := os.ReadFile(s.snapshotPath)
	if err != nil {
		return err
	}

	snapshot := make(map[string]*SymbolState)
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for symbol, state := range snapshot {
		s.symbols[symbol] = state
	}

	s.updateTotalNotionalLocked()

	log.Info().Str("path", s.snapshotPath).Int("symbols", len(snapshot)).Msg("快照加载成功")
	return nil
}

// runSnapshotLoop 运行快照循环
func (s *Store) runSnapshotLoop() {
	for {
		select {
		case <-s.snapshotTicker.C:
			if err := s.SaveSnapshot(); err != nil {
				s.lastSnapshotErr = err
				log.Error().Err(err).Msg("保存快照失败")
			}
		case <-s.stopSnapshot:
			return
		}
	}
}

// Close 关闭存储
func (s *Store) Close() {
	close(s.stopSnapshot)
	s.snapshotTicker.Stop()

	// 最后保存一次
	if err := s.SaveSnapshot(); err != nil {
		log.Error().Err(err).Msg("关闭时保存快照失败")
	}
}

// GetAllSymbols 获取所有交易对列表
func (s *Store) GetAllSymbols() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	symbols := make([]string, 0, len(s.symbols))
	for symbol := range s.symbols {
		symbols = append(symbols, symbol)
	}
	return symbols
}
