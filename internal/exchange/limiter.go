package gateway

import (
	"sync"
	"time"
)

// RateLimiter 控制请求速率，避免触发交易所限流。
type RateLimiter interface {
	Wait()
}

// TokenBucketLimiter 是一个简单的令牌桶实现。
type TokenBucketLimiter struct {
	rate   float64
	burst  int
	tokens float64
	last   time.Time
	mu     sync.Mutex
}

func NewTokenBucketLimiter(rate float64, burst int) *TokenBucketLimiter {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &TokenBucketLimiter{
		rate:   rate,
		burst:  burst,
		tokens: float64(burst),
		last:   time.Now(),
	}
}

func (l *TokenBucketLimiter) Wait() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(l.last).Seconds()
	l.last = now
	l.tokens += elapsed * l.rate
	if l.tokens > float64(l.burst) {
		l.tokens = float64(l.burst)
	}
	if l.tokens < 1 {
		sleep := time.Duration((1-l.tokens)/l.rate*float64(time.Second)) + time.Millisecond
		l.mu.Unlock()
		time.Sleep(sleep)
		l.mu.Lock()
		l.tokens = 0
	} else {
		l.tokens -= 1
	}
}

// CompositeLimiter 组合限速器：令牌桶 + 双窗硬上限（10s/60s）
type CompositeLimiter struct {
	tb            *TokenBucketLimiter
	window10sMax int
	window60sMax int
	mu           sync.Mutex
	recent       []time.Time
}

func NewCompositeLimiter(rate float64, burst int, max10s, max60s int) *CompositeLimiter {
	return &CompositeLimiter{
		tb:            NewTokenBucketLimiter(rate, burst),
		window10sMax: max10s,
		window60sMax: max60s,
		recent:       make([]time.Time, 0, 1024),
	}
}

func (l *CompositeLimiter) Wait() {
	for {
		now := time.Now()
		// 维护滑动窗口计数
		l.mu.Lock()
		cut10 := now.Add(-10 * time.Second)
		cut60 := now.Add(-60 * time.Second)
		pruned := l.recent[:0]
		cnt10 := 0
		cnt60 := 0
		for _, t := range l.recent {
			if t.After(cut60) {
				pruned = append(pruned, t)
				if t.After(cut10) {
					cnt10++
				}
				cnt60++
			}
		}
		l.recent = pruned
		over := (l.window10sMax > 0 && cnt10 >= l.window10sMax) || (l.window60sMax > 0 && cnt60 >= l.window60sMax)
		l.mu.Unlock()
		if over {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		// 令牌桶限速
		l.tb.Wait()
		// 记录本次请求
		l.mu.Lock()
		l.recent = append(l.recent, time.Now())
		l.mu.Unlock()
		return
	}
}
