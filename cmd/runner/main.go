package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/metrics"
	"github.com/newplayman/market-maker-phoenix/internal/risk"
	"github.com/newplayman/market-maker-phoenix/internal/runner"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/newplayman/market-maker-phoenix/internal/strategy"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	configFile = flag.String("config", "config.yaml", "配置文件路径")
	logLevel   = flag.String("log", "info", "日志级别 (debug, info, warn, error)")
)

func main() {
	flag.Parse()

	// 单实例锁实现，防止多进程启动
	lockFile := "/tmp/phoenix_runner.lock"
	lock, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatal().Err(err).Msg("创建锁文件失败")
	}
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		log.Fatal().Msg("已有一个Phoenix进程在运行")
	}
	defer func() {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
		os.Remove(lockFile)
	}()

	// 设置日志
	setupLogger(*logLevel)

	log.Info().Msg("Phoenix高频做市商系统 v2.0 启动中...")

	// 加载配置
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatal().Err(err).Msg("加载配置失败")
	}

	log.Info().
		Int("symbols", len(cfg.Symbols)).
		Float64("total_notional_max", cfg.Global.TotalNotionalMax).
		Msg("配置加载成功")

	// 创建上下文
	log.Info().Msg("创建上下文...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 初始化Store
	log.Info().Msg("初始化Store...")
	st := store.NewStore(
		cfg.Global.SnapshotPath,
		time.Duration(cfg.Global.SnapshotInterval)*time.Second,
	)
	defer st.Close()
	log.Info().Msg("Store初始化完成")

	// 初始化交易对状态
	log.Info().Msg("初始化交易对状态...")
	for _, symCfg := range cfg.Symbols {
		st.InitSymbol(symCfg.Symbol, 1800) // 30分钟价格历史（假设1秒1个数据点）
		log.Info().Str("symbol", symCfg.Symbol).Msg("交易对初始化完成")
	}

	// 创建策略
	log.Info().Msg("创建策略...")
	strat := strategy.NewASMM(cfg, st)
	log.Info().Msg("策略创建完成")

	// 创建风控管理器
	log.Info().Msg("创建风控管理器...")
	riskMgr := risk.NewRiskManager(cfg, st)
	log.Info().Msg("风控管理器创建完成")

	// 创建Exchange适配器
	log.Info().
		Bool("testnet", cfg.Global.TestNet).
		Msg("初始化Binance连接")

	// 创建REST客户端
	rest := &gateway.BinanceRESTClient{
		BaseURL:      gateway.BinanceFuturesRestEndpoint,
		APIKey:       cfg.Global.APIKey,
		Secret:       cfg.Global.APISecret,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		RecvWindowMs: 5000,
		Limiter:      gateway.NewCompositeLimiter(20, 100, 100, 1200), // rate=20/s, burst=100, max10s=100, max60s=1200
		MaxRetries:   3,
		RetryDelay:   time.Second,
	}

	// 创建WebSocket客户端
	ws := gateway.NewBinanceWSReal()

	// 创建适配器
	exchange := gateway.NewBinanceAdapter(rest, ws)

	// 启动Prometheus监控
	if err := metrics.StartMetricsServer(cfg.Global.MetricsPort); err != nil {
		log.Error().Err(err).Msg("启动监控服务器失败")
	}

	// 创建Runner
	r := runner.NewRunner(cfg, st, strat, riskMgr, exchange)

	// 启动Runner
	log.Info().Msg("正在启动Runner...")
	if err := r.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("启动Runner失败")
	}

	log.Info().Msg("Phoenix系统启动完成，开始做市...")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	log.Info().Msg("收到退出信号，正在关闭...")

	// 优雅关闭
	cancel()
	r.Stop()

	log.Info().Msg("Phoenix系统已关闭")
}

// setupLogger 设置日志
func setupLogger(level string) {
	// 设置日志格式为人类可读的格式
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.RFC3339,
	})

	// 设置日志级别
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
