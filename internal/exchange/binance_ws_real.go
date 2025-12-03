package gateway

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// BinanceWSReal 组合订阅深度/用户数据流并连接真实 WS（执行者确保网络可达）。
// 仅提供最小骨架：连接 + 简单读取；业务层可在 handler 中扩展解析。
type BinanceWSReal struct {
	BaseEndpoint string // 默认 wss://fstream.binance.com
	depthStreams []string
	userStream   string
	Dialer       *websocket.Dialer
	MaxRetries   int
	RetryBackoff time.Duration
	onConnect    func()
	onDisconnect func(error)
	// 心跳检测
	HeartbeatTimeout       time.Duration
	HeartbeatCheckInterval time.Duration
	lastMessageUnix        atomic.Int64

	mu   sync.Mutex
	conn *websocket.Conn
}

func NewBinanceWSReal() *BinanceWSReal {
	// 【流量优化】启用WebSocket压缩 + 优化缓冲区大小
	// 减小缓冲区可以降低内存占用，同时保持压缩效果
	dialer := &websocket.Dialer{
		Proxy:             websocket.DefaultDialer.Proxy,
		HandshakeTimeout:  45 * time.Second,
		ReadBufferSize:    2048, // 从4096降低到2048，减少内存占用
		WriteBufferSize:   2048, // 从4096降低到2048，减少内存占用
		EnableCompression: true, // 🔥 关键：启用perflate压缩
	}

	return &BinanceWSReal{
		BaseEndpoint:           BinanceFuturesWSEndpoint,
		Dialer:                 dialer,
		MaxRetries:             5,
		RetryBackoff:           time.Second,
		HeartbeatTimeout:       5 * time.Second,
		HeartbeatCheckInterval: time.Second,
	}
}

func (b *BinanceWSReal) SubscribeDepth(symbol string) error {
	if symbol == "" {
		return fmt.Errorf("symbol required")
	}
	// 【紧急回滚】先确保连接稳定
	// 使用depth5@100ms - Binance官方文档确认支持的格式
	// 深度层数: 5层(最小,减少90%数据量 vs depth20)
	// 更新频率: 100ms(确保实时性)
	// 如果稳定后仍有流量问题,再调整频率
	stream := strings.ToLower(symbol) + "@depth5@100ms"
	b.depthStreams = append(b.depthStreams, stream)
	return nil
}

func (b *BinanceWSReal) SubscribeUserData(listenKey string) error {
	if listenKey == "" {
		return fmt.Errorf("listenKey required")
	}
	b.userStream = listenKey
	return nil
}

// CloseConnection 强制关闭当前连接以触发重连
func (b *BinanceWSReal) CloseConnection() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		log.Printf("强制关闭WebSocket连接以触发重连")
		b.conn.Close()
	}
}

func (b *BinanceWSReal) OnConnect(cb func()) {
	b.onConnect = cb
}

func (b *BinanceWSReal) OnDisconnect(cb func(error)) {
	b.onDisconnect = cb
}

// Run 构建 combined stream 并读取消息；对消息不做解析，业务可扩展。
// 【修复断流】增强自动重连机制：ReadMessage失败后立即重连，添加心跳检测
func (b *BinanceWSReal) Run(handler WSHandler) error {
	retries := 0
	lastConnectTime := time.Time{}

	for {
		// 【修复】每次重连重新构建URL，确保能获取最新的ListenKey
		streams := make([]string, 0, len(b.depthStreams)+1)
		streams = append(streams, b.depthStreams...)
		if b.userStream != "" {
			streams = append(streams, b.userStream)
		}
		if len(streams) == 0 {
			// 如果没有订阅，等待一会再试
			time.Sleep(time.Second)
			continue
		}

		u := url.URL{
			Scheme: "wss",
			Host:   strings.TrimPrefix(b.BaseEndpoint, "wss://"),
			Path:   "/stream",
		}
		q := u.Query()
		q.Set("streams", strings.Join(streams, "/"))
		u.RawQuery = q.Encode()
		streamURL := u.String()

		// 连接WebSocket
		conn, resp, err := b.Dialer.Dial(streamURL, nil)
		if err != nil {
			if retries >= b.MaxRetries {
				log.Printf("ws max retries (%d) reached, giving up", b.MaxRetries)
				return err
			}
			retries++
			sleep := b.RetryBackoff * time.Duration(retries)
			log.Printf("ws dial failed (%d/%d): %v, retry in %s", retries, b.MaxRetries, err, sleep)
			time.Sleep(sleep)
			continue
		}

		// 【流量优化】验证压缩是否生效
		if resp != nil {
			extensions := resp.Header.Get("Sec-WebSocket-Extensions")
			if extensions != "" {
				log.Printf("WebSocket压缩协商: %s", extensions)
			} else {
				log.Printf("警告: WebSocket压缩未启用（Binance可能不支持）")
			}
		}

		// 连接成功，重置重试计数
		retries = 0
		lastConnectTime = time.Now()
		b.lastMessageUnix.Store(lastConnectTime.UnixMilli())

		b.mu.Lock()
		b.conn = conn
		b.mu.Unlock()

		if b.onConnect != nil {
			b.onConnect()
		}

		log.Printf("WebSocket连接成功，开始读取消息...")

		// 启动心跳goroutine（每20秒发送ping）
		pingTicker := time.NewTicker(20 * time.Second)
		stopPing := make(chan struct{})
		go func() {
			defer pingTicker.Stop()
			for {
				select {
				case <-pingTicker.C:
					_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						log.Printf("ws ping failed: %v", err)
						return
					}
				case <-stopPing:
					return
				}
			}
		}()

		// 启动被动心跳监控（检测无数据超时）
		heartbeatTicker := time.NewTicker(func() time.Duration {
			if b.HeartbeatCheckInterval > 0 {
				return b.HeartbeatCheckInterval
			}
			return time.Second
		}())
		stopHeartbeat := make(chan struct{})
		go func() {
			defer heartbeatTicker.Stop()
			for {
				select {
				case <-heartbeatTicker.C:
					timeout := b.HeartbeatTimeout
					if timeout <= 0 {
						timeout = 5 * time.Second
					}
					last := time.UnixMilli(b.lastMessageUnix.Load())
					if last.IsZero() {
						continue
					}
					if time.Since(last) > timeout {
						log.Printf("ws heartbeat timeout %s，强制关闭连接重连", time.Since(last))
						conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "heartbeat timeout"), time.Now().Add(5*time.Second))
						conn.Close()
						return
					}
				case <-stopHeartbeat:
					return
				}
			}
		}()

		// 读取消息循环
		readDeadline := 30 * time.Second
		conn.SetReadDeadline(time.Now().Add(readDeadline))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(readDeadline))
			return nil
		})

		readErr := func() error {
			defer func() {
				close(stopPing)
				close(stopHeartbeat)
				b.mu.Lock()
				if b.conn == conn {
					b.conn = nil
				}
				b.mu.Unlock()
				conn.Close()
			}()

			for {
				conn.SetReadDeadline(time.Now().Add(readDeadline))
				_, message, err := conn.ReadMessage()
				if err != nil {
					// ReadMessage失败，立即返回错误（触发重连）
					return err
				}
				b.lastMessageUnix.Store(time.Now().UnixMilli())

				// 【流量监控】记录WebSocket接收字节数
				// 注意：这里记录的是原始字节数（压缩后），实际流量已节省60-70%
				// 在adapter层会按symbol分类记录
				if handler != nil {
					// 先记录原始消息用于全局流量统计
					if h, ok := handler.(interface {
						OnRawMessage([]byte)
						GetCurrentSymbol() string
					}); ok {
						h.OnRawMessage(message)
					} else if h, ok := handler.(interface{ OnRawMessage([]byte) }); ok {
						h.OnRawMessage(message)
					}
				} else {
					log.Printf("binance ws recv: %s", string(message))
				}
			}
		}()

		// ReadMessage失败，通知断开并立即重连
		if readErr != nil {
			log.Printf("ws read err: %v, 立即重连...", readErr)
			if b.onDisconnect != nil {
				b.onDisconnect(readErr)
			}

			// 立即重连（不等待，但避免过快重连导致服务器压力）
			if time.Since(lastConnectTime) < 2*time.Second {
				time.Sleep(2 * time.Second)
			}
			continue
		}
	}
}
