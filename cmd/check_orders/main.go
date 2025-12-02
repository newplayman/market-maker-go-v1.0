package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	cfgPath := flag.String("config", "configs/phoenix_live.yaml", "配置文件路径")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("加载配置失败")
	}

	rest, _, _ := gateway.BuildRealBinanceClients(&http.Client{Timeout: 10 * time.Second})
	if rest == nil {
		log.Fatal().Msg("REST客户端构建失败")
	}

	for _, sym := range cfg.Symbols {
		symbol := sym.Symbol
		log.Info().Str("symbol", symbol).Msg("查询挂单...")
		orders, err := rest.OpenOrders(symbol)
		if err != nil {
			log.Error().Err(err).Msg("查询失败")
			continue
		}
		log.Info().Int("count", len(orders)).Msg("挂单数量")
		for _, o := range orders {
			fmt.Printf("Order: ID=%d ClientID=%s Price=%.2f Qty=%.3f\n", o.OrderID, o.ClientOrderID, o.Price, o.OrigQty)
		}
	}
}
