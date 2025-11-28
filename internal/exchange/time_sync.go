package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// TimeSync 管理与币安服务器的时间同步
type TimeSync struct {
	mu           sync.RWMutex
	offset       int64 // 本地时间与服务器时间的差值（毫秒）
	lastSync     time.Time
	syncInterval time.Duration
	baseURL      string
	httpClient   *http.Client
}

// NewTimeSync 创建时间同步器
func NewTimeSync(baseURL string) *TimeSync {
	return &TimeSync{
		syncInterval: 30 * time.Minute, // 每30分钟同步一次
		baseURL:      baseURL,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Sync 从币安服务器同步时间
func (ts *TimeSync) Sync() error {
	resp, err := ts.httpClient.Get(ts.baseURL + "/fapi/v1/time")
	if err != nil {
		return fmt.Errorf("获取服务器时间失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("服务器返回错误 %d: %s", resp.StatusCode, body)
	}

	var result struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析服务器时间失败: %w", err)
	}

	localTime := time.Now().UnixMilli()
	offset := result.ServerTime - localTime

	ts.mu.Lock()
	ts.offset = offset
	ts.lastSync = time.Now()
	ts.mu.Unlock()

	return nil
}

// GetServerTime 返回同步后的服务器时间（毫秒）
func (ts *TimeSync) GetServerTime() int64 {
	ts.mu.RLock()
	offset := ts.offset
	lastSync := ts.lastSync
	ts.mu.RUnlock()

	// 如果从未同步或超过同步间隔，触发后台同步
	if lastSync.IsZero() || time.Since(lastSync) > ts.syncInterval {
		go ts.Sync()
	}

	return time.Now().UnixMilli() + offset
}

// GetOffset 返回当前时间偏移量（毫秒）
func (ts *TimeSync) GetOffset() int64 {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.offset
}
