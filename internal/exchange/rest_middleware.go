package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RESTMiddleware REST 请求中间件，统一处理签名/限流/重试/错误分类
type RESTMiddleware struct {
	client     *BinanceRESTClient
	retryLogic RetryLogic
}

// RetryLogic 重试策略
type RetryLogic struct {
	MaxAttempts     int
	InitialDelay    time.Duration
	MaxDelay        time.Duration
	BackoffFactor   float64
	RetryableErrors map[int]bool // HTTP 状态码
}

// DefaultRetryLogic 默认重试策略
func DefaultRetryLogic() RetryLogic {
	return RetryLogic{
		MaxAttempts:   3,
		InitialDelay:  200 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		RetryableErrors: map[int]bool{
			408: true, // Request Timeout
			418: true, // I'm a teapot (Binance rate limit)
			429: true, // Too Many Requests
			500: true, // Internal Server Error
			502: true, // Bad Gateway
			503: true, // Service Unavailable
			504: true, // Gateway Timeout
		},
	}
}

// NewRESTMiddleware 创建 REST 中间件
func NewRESTMiddleware(client *BinanceRESTClient) *RESTMiddleware {
	return &RESTMiddleware{
		client:     client,
		retryLogic: DefaultRetryLogic(),
	}
}

// ExecuteWithRetry 执行请求并自动重试
func (m *RESTMiddleware) ExecuteWithRetry(method, endpoint string, headers map[string]string) (*http.Response, error) {
	delay := m.retryLogic.InitialDelay
	var lastErr error

	for attempt := 0; attempt < m.retryLogic.MaxAttempts; attempt++ {
		// 等待限流器
		if m.client.Limiter != nil {
			m.client.Limiter.Wait()
		}

		// 执行请求
		req, err := http.NewRequest(method, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := m.client.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			// 网络错误，退避重试
			if attempt < m.retryLogic.MaxAttempts-1 {
				time.Sleep(delay)
				delay = m.calculateNextDelay(delay)
			}
			continue
		}

		// 检查是否需要重试
		if m.shouldRetry(resp.StatusCode) {
			lastErr = fmt.Errorf("retriable status %d", resp.StatusCode)
			resp.Body.Close()

			// 处理特殊限流响应
			retryAfter := m.getRetryAfter(resp)
			if retryAfter > 0 {
				delay = retryAfter
			}

			if attempt < m.retryLogic.MaxAttempts-1 {
				time.Sleep(delay)
				delay = m.calculateNextDelay(delay)
			}
			continue
		}

		// 成功或非重试错误，直接返回
		return resp, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", m.retryLogic.MaxAttempts, lastErr)
}

// shouldRetry 判断是否应该重试
func (m *RESTMiddleware) shouldRetry(statusCode int) bool {
	return m.retryLogic.RetryableErrors[statusCode]
}

// getRetryAfter 从响应头获取 Retry-After 时间
func (m *RESTMiddleware) getRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	// 尝试解析为秒数
	var seconds int
	if _, err := fmt.Sscanf(retryAfter, "%d", &seconds); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// 尝试解析为 HTTP-date
	if t, err := http.ParseTime(retryAfter); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return duration
		}
	}

	return 0
}

// calculateNextDelay 计算下一次重试延迟（指数退避）
func (m *RESTMiddleware) calculateNextDelay(currentDelay time.Duration) time.Duration {
	nextDelay := time.Duration(float64(currentDelay) * m.retryLogic.BackoffFactor)
	if nextDelay > m.retryLogic.MaxDelay {
		return m.retryLogic.MaxDelay
	}
	return nextDelay
}

// ClassifyError 分类 REST 错误
func ClassifyError(statusCode int, body string) ErrorType {
	switch {
	case statusCode == 401 || statusCode == 403:
		return ErrorTypeAuth
	case statusCode == 429 || statusCode == 418:
		return ErrorTypeRateLimit
	case statusCode >= 500:
		return ErrorTypeServer
	case statusCode >= 400 && statusCode < 500:
		// 检查特定错误码
		if strings.Contains(body, "-1021") { // Timestamp outside of recvWindow
			return ErrorTypeAuth
		}
		if strings.Contains(body, "-5022") { // Post-only reject
			return ErrorTypePostOnlyReject
		}
		if strings.Contains(body, "-2010") { // Insufficient balance
			return ErrorTypeInsufficientBalance
		}
		return ErrorTypeClient
	default:
		return ErrorTypeUnknown
	}
}

// ErrorType 错误类型
type ErrorType int

const (
	ErrorTypeUnknown ErrorType = iota
	ErrorTypeAuth
	ErrorTypeRateLimit
	ErrorTypeServer
	ErrorTypeClient
	ErrorTypePostOnlyReject
	ErrorTypeInsufficientBalance
)

// String 返回错误类型字符串
func (e ErrorType) String() string {
	switch e {
	case ErrorTypeAuth:
		return "auth_error"
	case ErrorTypeRateLimit:
		return "rate_limit"
	case ErrorTypeServer:
		return "server_error"
	case ErrorTypeClient:
		return "client_error"
	case ErrorTypePostOnlyReject:
		return "postonly_reject"
	case ErrorTypeInsufficientBalance:
		return "insufficient_balance"
	default:
		return "unknown"
	}
}

// IsRetriable 判断错误是否可重试
func (e ErrorType) IsRetriable() bool {
	switch e {
	case ErrorTypeRateLimit, ErrorTypeServer:
		return true
	default:
		return false
	}
}
