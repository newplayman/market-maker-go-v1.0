package gateway

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSReconnectConfig WebSocket 重连配置
type WSReconnectConfig struct {
	MaxRetries      int           // 最大重试次数（0=无限）
	InitialDelay    time.Duration // 初始重连延迟
	MaxDelay        time.Duration // 最大重连延迟
	BackoffFactor   float64       // 退避系数
	PingInterval    time.Duration // 心跳间隔
	PongWait        time.Duration // Pong 等待时间
	WriteWait       time.Duration // 写超时
	EnableHeartbeat bool          // 启用心跳
}

// DefaultWSReconnectConfig 默认配置
func DefaultWSReconnectConfig() WSReconnectConfig {
	return WSReconnectConfig{
		MaxRetries:      0, // 无限重试
		InitialDelay:    1 * time.Second,
		MaxDelay:        60 * time.Second,
		BackoffFactor:   2.0,
		PingInterval:    20 * time.Second,
		PongWait:        30 * time.Second,
		WriteWait:       10 * time.Second,
		EnableHeartbeat: true,
	}
}

// WSReconnectManager WebSocket 重连管理器
type WSReconnectManager struct {
	mu sync.RWMutex

	config        WSReconnectConfig
	conn          *websocket.Conn
	url           string
	connected     bool
	reconnecting  bool
	stopChan      chan struct{}
	doneChan      chan struct{}
	reconnectChan chan struct{}

	// 回调
	onConnect    func(*websocket.Conn)
	onDisconnect func(error)
	onMessage    func([]byte)
	onError      func(error)

	// 统计
	totalReconnects int
	lastConnectTime time.Time
}

// NewWSReconnectManager 创建重连管理器
func NewWSReconnectManager(url string, config WSReconnectConfig) *WSReconnectManager {
	return &WSReconnectManager{
		url:           url,
		config:        config,
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
		reconnectChan: make(chan struct{}, 1),
	}
}

// SetCallbacks 设置回调函数
func (m *WSReconnectManager) SetCallbacks(
	onConnect func(*websocket.Conn),
	onDisconnect func(error),
	onMessage func([]byte),
	onError func(error),
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnect = onConnect
	m.onDisconnect = onDisconnect
	m.onMessage = onMessage
	m.onError = onError
}

// Start 启动连接
func (m *WSReconnectManager) Start() error {
	m.mu.Lock()
	if m.connected || m.reconnecting {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	go m.run()
	return nil
}

// Stop 停止连接
func (m *WSReconnectManager) Stop() error {
	m.mu.Lock()
	if !m.connected && !m.reconnecting {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	close(m.stopChan)
	<-m.doneChan
	return nil
}

// TriggerReconnect 触发手动重连
func (m *WSReconnectManager) TriggerReconnect() {
	select {
	case m.reconnectChan <- struct{}{}:
	default:
	}
}

// IsConnected 是否已连接
func (m *WSReconnectManager) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// GetStats 获取统计信息
func (m *WSReconnectManager) GetStats() WSStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return WSStats{
		Connected:       m.connected,
		TotalReconnects: m.totalReconnects,
		LastConnectTime: m.lastConnectTime,
	}
}

// WSStats WebSocket 统计
type WSStats struct {
	Connected       bool
	TotalReconnects int
	LastConnectTime time.Time
}

// run 主循环
func (m *WSReconnectManager) run() {
	defer close(m.doneChan)

	delay := m.config.InitialDelay
	retries := 0

	for {
		// 尝试连接
		if err := m.connect(); err != nil {
			log.Printf("WS connect failed: %v", err)
			if m.onError != nil {
				m.onError(err)
			}

			// 检查是否达到最大重试次数
			if m.config.MaxRetries > 0 && retries >= m.config.MaxRetries {
				log.Printf("WS max retries (%d) reached, giving up", m.config.MaxRetries)
				return
			}

			retries++
			select {
			case <-m.stopChan:
				return
			case <-time.After(delay):
				delay = m.calculateNextDelay(delay)
				continue
			}
		}

		// 连接成功，重置重试计数
		retries = 0
		delay = m.config.InitialDelay

		m.mu.Lock()
		m.connected = true
		m.lastConnectTime = time.Now()
		m.mu.Unlock()

		if m.onConnect != nil {
			m.onConnect(m.conn)
		}

		// 读取消息
		err := m.readLoop()

		m.mu.Lock()
		m.connected = false
		m.mu.Unlock()

		if m.onDisconnect != nil {
			m.onDisconnect(err)
		}

		// 检查是否主动停止
		select {
		case <-m.stopChan:
			m.closeConn()
			return
		default:
		}

		// 等待重连
		log.Printf("WS disconnected, reconnecting in %v...", delay)
		select {
		case <-m.stopChan:
			m.closeConn()
			return
		case <-m.reconnectChan:
			// 立即重连
		case <-time.After(delay):
		}

		m.closeConn()
		delay = m.calculateNextDelay(delay)
	}
}

// connect 建立连接
func (m *WSReconnectManager) connect() error {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(m.url, nil)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.conn = conn
	m.totalReconnects++
	m.mu.Unlock()

	// 设置 Pong 处理器
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(m.config.PongWait))
		return nil
	})

	// 启动心跳
	if m.config.EnableHeartbeat {
		go m.heartbeatLoop()
	}

	return nil
}

// readLoop 读取消息循环
func (m *WSReconnectManager) readLoop() error {
	conn := m.getConn()
	if conn == nil {
		return nil
	}

	// 设置初始读超时
	_ = conn.SetReadDeadline(time.Now().Add(m.config.PongWait))

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		// 重置读超时
		_ = conn.SetReadDeadline(time.Now().Add(m.config.PongWait))

		// 处理消息
		if m.onMessage != nil {
			m.onMessage(message)
		}
	}
}

// heartbeatLoop 心跳循环
func (m *WSReconnectManager) heartbeatLoop() {
	ticker := time.NewTicker(m.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			conn := m.getConn()
			if conn == nil {
				return
			}

			_ = conn.SetWriteDeadline(time.Now().Add(m.config.WriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("WS heartbeat failed: %v", err)
				m.TriggerReconnect()
				return
			}
		}
	}
}

// calculateNextDelay 计算下一次重连延迟
func (m *WSReconnectManager) calculateNextDelay(currentDelay time.Duration) time.Duration {
	nextDelay := time.Duration(float64(currentDelay) * m.config.BackoffFactor)
	if nextDelay > m.config.MaxDelay {
		return m.config.MaxDelay
	}
	return nextDelay
}

// getConn 获取连接
func (m *WSReconnectManager) getConn() *websocket.Conn {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.conn
}

// closeConn 关闭连接
func (m *WSReconnectManager) closeConn() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
}
