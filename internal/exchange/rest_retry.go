package gateway

import (
	"fmt"
	"strings"
	"time"
)

// RetryConfig 重试配置
type RetryConfig struct {
	MaxRetries int           // 最大重试次数
	BaseDelay  time.Duration // 基础延迟
	MaxDelay   time.Duration // 最大延迟
}

// DefaultRetryConfig 返回默认重试配置
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   10 * time.Second,
	}
}

// WithRetry 使用指数退避策略执行函数，支持重试
func WithRetry(fn func() error, cfg RetryConfig) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		lastErr = fn()

		// 成功，直接返回
		if lastErr == nil {
			return nil
		}

		// 检查是否可重试
		if !isRetryableError(lastErr) {
			return lastErr
		}

		// 如果还有重试机会，等待后重试
		if attempt < cfg.MaxRetries {
			delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay)
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("max retries (%d) exceeded: %w", cfg.MaxRetries, lastErr)
}

// calculateBackoff 计算指数退避延迟
func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	// 指数退避: baseDelay * 2^attempt
	delay := baseDelay * time.Duration(1<<uint(attempt))

	// 限制最大延迟
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// 网络相关错误可重试
	retryablePatterns := []string{
		"timeout",
		"connection reset",
		"connection refused",
		"temporary failure",
		"too many requests",
		"rate limit",
		"503", // Service Unavailable
		"502", // Bad Gateway
		"504", // Gateway Timeout
		"429", // Too Many Requests
	}

	errLower := strings.ToLower(errStr)
	for _, pattern := range retryablePatterns {
		if strings.Contains(errLower, pattern) {
			return true
		}
	}

	// 以下错误不可重试（业务逻辑错误）
	nonRetryablePatterns := []string{
		"invalid api-key",
		"signature",
		"unauthorized",
		"forbidden",
		"400", // Bad Request
		"401", // Unauthorized
		"403", // Forbidden
		"404", // Not Found
	}

	for _, pattern := range nonRetryablePatterns {
		if strings.Contains(errLower, pattern) {
			return false
		}
	}

	// 默认可重试
	return true
}
