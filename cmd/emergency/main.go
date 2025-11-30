package main

import (
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func setupLogger(level string) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func main() {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	logLevel := flag.String("log", "info", "日志级别 (debug, info, warn, error)")
	flag.Parse()

	setupLogger(*logLevel)
	log.Info().Msg("Phoenix 紧急刹车工具启动...")

	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("加载配置失败")
	}

	// 构建真实客户端（含时间同步）
	rest, _, _ := gateway.BuildRealBinanceClients(&http.Client{Timeout: 10 * time.Second})
	if rest == nil {
		log.Fatal().Msg("REST客户端构建失败")
	}

	// 先撤销所有挂单
	for _, sym := range cfg.Symbols {
		symbol := sym.Symbol
		log.Info().Str("symbol", symbol).Msg("撤销所有挂单...")
		if err := rest.CancelAll(symbol); err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("撤单失败")
		}
	}

	// 按持仓方向使用 Reduce-Only 市价单平仓
	for _, sym := range cfg.Symbols {
		symbol := sym.Symbol

		positions, err := rest.PositionRisk(symbol)
		if err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("查询持仓失败")
			continue
		}

		var posAmt float64
		for _, p := range positions {
			if p.Symbol == symbol {
				posAmt += p.PositionAmt
			}
		}

		if posAmt == 0 {
			log.Info().Str("symbol", symbol).Msg("无需平仓：仓位为0")
			continue
		}

		qty := math.Abs(posAmt)
		side := "SELL"
		if posAmt < 0 {
			side = "BUY"
		}

		// 生成符合交易所要求的客户端订单ID，使用毫秒时间戳
		clientID := fmt.Sprintf("phoenix-emg-%d", time.Now().UnixMilli())
		log.Warn().Str("symbol", symbol).Str("side", side).Float64("qty", qty).Msg("使用Reduce-Only市价单平仓...")
		if _, err := rest.PlaceMarket(symbol, side, qty, true, clientID); err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("平仓下单失败")
		} else {
			log.Info().Str("symbol", symbol).Msg("平仓下单已提交")
		}
	}

	log.Info().Msg("Phoenix 紧急刹车完成。")
}