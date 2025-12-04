package runner

import (
	"context"
	"sync/atomic"
	"time"

	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/metrics"
	"github.com/rs/zerolog/log"
)

// runDepthProcessor 【P1-1】处理深度消息的独立goroutine
// 从channel中取出消息处理,解耦WebSocket接收和业务处理,防止背压
func (r *Runner) runDepthProcessor(ctx context.Context, workerID int) {
	defer r.wg.Done()

	log.Info().
		Int("worker_id", workerID).
		Msg("【P1-1】深度消息处理器已启动")

	for {
		select {
		case <-ctx.Done():
			log.Info().
				Int("worker_id", workerID).
				Msg("深度处理器收到退出信号(ctx)")
			return
		case <-r.stopChan:
			log.Info().
				Int("worker_id", workerID).
				Msg("深度处理器收到停止信号")
			return
		case depth, ok := <-r.depthChan:
			if !ok {
				log.Info().
					Int("worker_id", workerID).
					Msg("深度处理器检测到通道关闭，退出")
				return
			}
			// 【紧急修复】主动丢弃堆积的旧消息
			// 如果channel中还有很多消息,说明处理跟不上,丢弃旧的只保留最新
			channelLen := len(r.depthChan)
			if channelLen > 50 { // 超过50条堆积
				droppedCount := 0
				// 快速清空到只剩10条
				for len(r.depthChan) > 10 {
					<-r.depthChan // 丢弃
					droppedCount++
				}
				if droppedCount > 0 {
					atomic.AddInt64(&r.depthDropCount, int64(droppedCount))
					log.Warn().
						Int("worker_id", workerID).
						Int("dropped", droppedCount).
						Int("remaining", len(r.depthChan)).
						Msg("【紧急修复】主动丢弃堆积的旧深度消息")
				}
			}

			// 处理深度消息
			r.processDepthMessage(depth)
		}
	}
}

// processDepthMessage 【P1-1】实际处理深度消息的逻辑
func (r *Runner) processDepthMessage(depth *gateway.Depth) {
	// 【P0-2】添加耗时监控
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		if depth != nil {
			metrics.DepthProcessing.WithLabelValues(depth.Symbol).Observe(duration)

			// 如果处理耗时超过100ms,记录警告
			if duration > 0.1 {
				log.Warn().
					Str("symbol", depth.Symbol).
					Float64("duration_ms", duration*1000).
					Msg("深度处理耗时过长（>100ms），可能导致背压")
			}
		}
	}()

	if depth == nil || len(depth.Bids) == 0 || len(depth.Asks) == 0 {
		return
	}

	// 更新Store中的市场数据
	bestBid := depth.Bids[0].Price
	bestAsk := depth.Asks[0].Price
	midPrice := (bestBid + bestAsk) / 2.0

	r.store.UpdateMidPrice(depth.Symbol, midPrice, bestBid, bestAsk)

	log.Debug().
		Str("symbol", depth.Symbol).
		Float64("mid", midPrice).
		Float64("bid", bestBid).
		Float64("ask", bestAsk).
		Msg("深度更新")
}
