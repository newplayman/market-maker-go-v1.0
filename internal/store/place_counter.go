package store

import "time"

// IncrementPlaceCount 【P1-4】增加下单计数
func (s *Store) IncrementPlaceCount(symbol string) int {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	// 检查是否需要重置计数（每分钟）
	if time.Since(state.LastPlaceReset) > time.Minute {
		state.PlaceCountLast = 0
		state.LastPlaceReset = time.Now()
	}

	state.PlaceCountLast++
	return state.PlaceCountLast
}

// GetPlaceCount 【P1-4】获取当前下单计数
func (s *Store) GetPlaceCount(symbol string) int {
	s.mu.RLock()
	state := s.symbols[symbol]
	s.mu.RUnlock()

	if state == nil {
		return 0
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	return state.PlaceCountLast
}
