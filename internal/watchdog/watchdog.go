package watchdog

import (
	"context"
	"sync"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/rs/zerolog/log"
)

// RestPinger 定义REST心跳能力
type RestPinger interface {
	Ping() error
}

// Hooks Runner 需要实现的自恢复动作
type Hooks interface {
	EnterSafeMode(reason string)
	ExitSafeMode(reason string)
	ForceResync(reason string)
	ForceWebSocketReconnect(reason string)
}

// Config 看门狗配置
type Config struct {
	RestPingInterval      time.Duration
	RestFailureThreshold  int
	RestRecoveryThreshold int

	WsCheckInterval     time.Duration
	WsStaleThreshold    time.Duration
	WsFailureThreshold  int
	WsRecoveryThreshold int
}

func (c *Config) normalize() {
	if c.RestPingInterval <= 0 {
		c.RestPingInterval = 15 * time.Second
	}
	if c.RestFailureThreshold <= 0 {
		c.RestFailureThreshold = 3
	}
	if c.RestRecoveryThreshold <= 0 {
		c.RestRecoveryThreshold = 2
	}
	if c.WsCheckInterval <= 0 {
		c.WsCheckInterval = 5 * time.Second
	}
	if c.WsStaleThreshold <= 0 {
		c.WsStaleThreshold = 6 * time.Second
	}
	if c.WsFailureThreshold <= 0 {
		c.WsFailureThreshold = 3
	}
	if c.WsRecoveryThreshold <= 0 {
		c.WsRecoveryThreshold = 2
	}
}

// Watchdog 监控REST/WS状态并触发自恢复
type Watchdog struct {
	cfg   Config
	rest  RestPinger
	store *store.Store
	hooks Hooks

	cancel context.CancelFunc
	wg     sync.WaitGroup

	restFailures   int
	restRecoveries int
	restUnhealthy  bool

	wsFailures   int
	wsRecoveries int
	wsUnhealthy  bool
}

// NewWatchdog 创建看门狗
func NewWatchdog(cfg Config, rest RestPinger, st *store.Store, hooks Hooks) *Watchdog {
	cfg.normalize()
	return &Watchdog{
		cfg:   cfg,
		rest:  rest,
		store: st,
		hooks: hooks,
	}
}

// Start 启动看门狗
func (w *Watchdog) Start(ctx context.Context) {
	if w.store == nil || w.hooks == nil {
		log.Warn().Msg("watchdog 未启用：缺少 store 或 hooks")
		return
	}

	childCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	if w.rest != nil {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.runRestLoop(childCtx)
		}()
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runWsLoop(childCtx)
	}()
}

// Stop 停止看门狗
func (w *Watchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
		w.wg.Wait()
	}
}

func (w *Watchdog) runRestLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.RestPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.rest.Ping(); err != nil {
				w.restFailures++
				w.restRecoveries = 0
				log.Error().Err(err).Msg("REST心跳失败")
				if w.restFailures >= w.cfg.RestFailureThreshold && !w.restUnhealthy {
					w.restUnhealthy = true
					log.Error().Msg("REST连续失败，进入安全模式")
					w.hooks.EnterSafeMode("rest_unreachable")
				}
			} else {
				if w.restUnhealthy {
					w.restRecoveries++
					if w.restRecoveries >= w.cfg.RestRecoveryThreshold {
						w.restUnhealthy = false
						log.Info().Msg("REST恢复，退出安全模式并同步状态")
						w.hooks.ForceResync("rest_recovered")
						w.hooks.ExitSafeMode("rest_recovered")
					}
				}
				w.restFailures = 0
			}
		}
	}
}

func (w *Watchdog) runWsLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.WsCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			staleSymbols := w.detectStaleSymbols()
			if len(staleSymbols) > 0 {
				w.wsFailures++
				w.wsRecoveries = 0
				log.Error().
					Strs("symbols", staleSymbols).
					Dur("stale_threshold", w.cfg.WsStaleThreshold).
					Msg("WebSocket长时间无数据，触发重连")
				w.hooks.ForceWebSocketReconnect("ws_stale")
				if w.wsFailures >= w.cfg.WsFailureThreshold && !w.wsUnhealthy {
					w.wsUnhealthy = true
					w.hooks.EnterSafeMode("websocket_stale")
				}
			} else {
				w.wsFailures = 0
				if w.wsUnhealthy {
					w.wsRecoveries++
					if w.wsRecoveries >= w.cfg.WsRecoveryThreshold {
						w.wsUnhealthy = false
						log.Info().Msg("WebSocket恢复，退出安全模式并同步状态")
						w.hooks.ForceResync("ws_recovered")
						w.hooks.ExitSafeMode("ws_recovered")
					}
				}
			}
		}
	}
}

func (w *Watchdog) detectStaleSymbols() []string {
	now := time.Now()
	var stale []string

	for _, symbol := range w.store.GetAllSymbols() {
		state := w.store.GetSymbolState(symbol)
		if state == nil {
			continue
		}
		state.Mu.RLock()
		lastUpdate := state.LastPriceUpdate
		state.Mu.RUnlock()
		if lastUpdate.IsZero() {
			continue
		}
		if now.Sub(lastUpdate) > w.cfg.WsStaleThreshold {
			stale = append(stale, symbol)
		}
	}
	return stale
}
