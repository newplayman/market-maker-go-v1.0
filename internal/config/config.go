package config

import (
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// Config 全局配置结构
type Config struct {
	Global  GlobalConfig   `mapstructure:"global"`
	Symbols []SymbolConfig `mapstructure:"symbols"`
}

// GlobalConfig 全局配置
type GlobalConfig struct {
	TotalNotionalMax float64 `mapstructure:"total_notional_max"` // 总名义价值上限 ($)
	QuoteIntervalMs  int     `mapstructure:"quote_interval_ms"`  // 报价间隔 (ms)
	APIKey           string  `mapstructure:"api_key"`            // Binance API Key
	APISecret        string  `mapstructure:"api_secret"`         // Binance API Secret
	TestNet          bool    `mapstructure:"testnet"`            // 是否使用测试网
	LogLevel         string  `mapstructure:"log_level"`          // 日志级别
	MetricsPort      int     `mapstructure:"metrics_port"`       // Prometheus 端口
	SnapshotPath     string  `mapstructure:"snapshot_path"`      // 快照文件路径
	SnapshotInterval int     `mapstructure:"snapshot_interval"`  // 快照保存间隔 (秒)
}

// SymbolConfig 单个交易对配置
type SymbolConfig struct {
	Symbol           string  `mapstructure:"symbol"`             // 交易对符号 (e.g., ETHUSDC)
	NetMax           float64 `mapstructure:"net_max"`            // 最大净仓位 (手数)
	MinSpread        float64 `mapstructure:"min_spread"`         // 最小价差 (比例)
	TickSize         float64 `mapstructure:"tick_size"`          // 价格最小变动单位
	MinQty           float64 `mapstructure:"min_qty"`            // 最小下单量
	BaseLayerSize    float64 `mapstructure:"base_layer_size"`    // 基础层级挂单量（废弃，使用UnifiedLayerSize）
	NearLayers       int     `mapstructure:"near_layers"`        // 近端层数（废弃，使用TotalLayers）
	FarLayers        int     `mapstructure:"far_layers"`         // 远端层数（废弃，使用TotalLayers）
	FarLayerSize     float64 `mapstructure:"far_layer_size"`     // 远端层挂单量（废弃）
	PinningEnabled   bool    `mapstructure:"pinning_enabled"`    // 是否启用钉子模式
	PinningThresh    float64 `mapstructure:"pinning_thresh"`     // 钉子模式触发阈值 (净仓/NetMax)
	GrindingEnabled  bool    `mapstructure:"grinding_enabled"`   // 是否启用磨仓模式
	GrindingThresh   float64 `mapstructure:"grinding_thresh"`    // 磨仓触发阈值
	StopLossThresh   float64 `mapstructure:"stop_loss_thresh"`   // 止损阈值 (回撤%)
	MaxCancelPerMin  int     `mapstructure:"max_cancel_per_min"` // 每分钟最大撤单数
	LayerSpacingMode string  `mapstructure:"layer_spacing_mode"` // 层间距模式: geometric(几何) | linear(线性)
	SpacingRatio     float64 `mapstructure:"spacing_ratio"`      // 几何增长公比（废弃，使用GridSpacingMultiplier）

	// 【新增】统一几何网格参数
	TotalLayers           int     `mapstructure:"total_layers"`            // 总层数（单边，例如18）
	GridStartOffset       float64 `mapstructure:"grid_start_offset"`       // 第一层距离mid（USDT，例如1.2）
	GridFirstSpacing      float64 `mapstructure:"grid_first_spacing"`      // 第一层间距（USDT，例如1.2）
	GridSpacingMultiplier float64 `mapstructure:"grid_spacing_multiplier"` // 几何系数（例如1.15）
	GridMaxSpacing        float64 `mapstructure:"grid_max_spacing"`        // 最大层间距（USDT，例如25）
	UnifiedLayerSize      float64 `mapstructure:"unified_layer_size"`      // 统一层大小（ETH，例如0.0067 ≈ 20U @ 3000价格）

	// 近端层级参数 - 实现更紧的盘口报价（废弃，使用统一网格）
	NearLayerStartOffset  float64 `mapstructure:"near_layer_start_offset"`  // 近端起始偏移 (比例，如0.00033表示0.033%)
	NearLayerSpacingRatio float64 `mapstructure:"near_layer_spacing_ratio"` // 近端层间距几何公比

	// 远端层级参数 - 实现几何网格（废弃，使用统一网格）
	FarLayerStartOffset float64 `mapstructure:"far_layer_start_offset"` // 远端起始偏移 (比例)
	FarLayerEndOffset   float64 `mapstructure:"far_layer_end_offset"`   // 远端结束偏移 (比例)

	// 库存偏移系数 - 增强成交后逼近盘口的效果
	InventorySkewCoeff float64 `mapstructure:"inventory_skew_coeff"` // 库存偏移系数 (默认0.002)

	// VPIN配置（可选，默认禁用）
	VPINEnabled       bool    `mapstructure:"vpin_enabled"`        // 是否启用VPIN
	VPINBucketSize    float64 `mapstructure:"vpin_bucket_size"`    // Bucket大小（成交量）
	VPINNumBuckets    int     `mapstructure:"vpin_num_buckets"`    // Bucket数量
	VPINThreshold     float64 `mapstructure:"vpin_threshold"`      // 警报阈值
	VPINPauseThresh   float64 `mapstructure:"vpin_pause_thresh"`   // 暂停阈值
	VPINMultiplier    float64 `mapstructure:"vpin_multiplier"`     // Spread放大系数
	VPINVolThreshold  float64 `mapstructure:"vpin_vol_threshold"`  // 最小成交量要求
}

var (
	globalConfig *Config
	configPath   string
)

// LoadConfig 加载配置文件
func LoadConfig(path string) (*Config, error) {
	configPath = path
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	// 环境变量覆盖
	viper.AutomaticEnv()
	viper.SetEnvPrefix("PHOENIX")
	// 显式绑定嵌套字段的环境变量（生产推荐）
	viper.BindEnv("global.api_key", "BINANCE_API_KEY")
	viper.BindEnv("global.api_secret", "BINANCE_API_SECRET")
	viper.BindEnv("global.testnet", "BINANCE_TESTNET")
	viper.BindEnv("global.metrics_port", "PHOENIX_METRICS_PORT")
	viper.BindEnv("global.snapshot_path", "PHOENIX_SNAPSHOT_PATH")
	viper.BindEnv("global.snapshot_interval", "PHOENIX_SNAPSHOT_INTERVAL")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 验证配置
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	globalConfig = &cfg

	// 启动热重载监听
	go watchConfig()

	log.Info().Str("path", path).Msg("配置加载成功")
	return &cfg, nil
}

// GetConfig 获取全局配置
func GetConfig() *Config {
	return globalConfig
}

// validateConfig 验证配置有效性
func validateConfig(cfg *Config) error {
	// 全局配置验证
	if cfg.Global.TotalNotionalMax <= 0 {
		return fmt.Errorf("total_notional_max 必须 > 0")
	}
	if cfg.Global.QuoteIntervalMs < 100 || cfg.Global.QuoteIntervalMs > 5000 {
		return fmt.Errorf("quote_interval_ms 必须在 100-5000 之间")
	}
	if cfg.Global.APIKey == "" || cfg.Global.APISecret == "" {
		return fmt.Errorf("API Key 和 Secret 不能为空")
	}

	// 交易对配置验证
	if len(cfg.Symbols) == 0 {
		return fmt.Errorf("至少需要配置一个交易对")
	}

	for i := range cfg.Symbols {
		sym := &cfg.Symbols[i] // 使用指针以便修改原始配置
		if sym.Symbol == "" {
			return fmt.Errorf("symbols[%d]: symbol 不能为空", i)
		}
		if sym.NetMax <= 0.1 {
			return fmt.Errorf("symbols[%d]: net_max 必须 > 0.1", i)
		}
		if sym.MinSpread <= 0 || sym.MinSpread > 0.01 {
			return fmt.Errorf("symbols[%d]: min_spread 必须在 (0, 0.01] 之间", i)
		}

		// 【新增】统一几何网格配置验证和兼容处理
		// 优先使用新配置，如果未设置则使用旧配置
		if sym.TotalLayers == 0 {
			sym.TotalLayers = sym.NearLayers + sym.FarLayers
			if sym.TotalLayers == 0 {
				return fmt.Errorf("symbols[%d]: total_layers 或 (near_layers + far_layers) 必须配置", i)
			}
		}
		// 验证总层数范围
		if sym.TotalLayers < 1 || sym.TotalLayers > 50 {
			return fmt.Errorf("symbols[%d]: total_layers 必须在 1-50 之间", i)
		}

		// 统一层大小兼容处理
		if sym.UnifiedLayerSize == 0 {
			if sym.BaseLayerSize > 0 {
				sym.UnifiedLayerSize = sym.BaseLayerSize
			} else {
				return fmt.Errorf("symbols[%d]: unified_layer_size 或 base_layer_size 必须配置", i)
			}
		}
		if sym.UnifiedLayerSize <= 0 {
			return fmt.Errorf("symbols[%d]: unified_layer_size 必须 > 0", i)
		}

		// 几何网格参数验证（仅在配置了新参数时验证）
		if sym.GridStartOffset > 0 || sym.GridFirstSpacing > 0 || sym.GridSpacingMultiplier > 0 {
			if sym.GridStartOffset <= 0 {
				return fmt.Errorf("symbols[%d]: grid_start_offset 必须 > 0", i)
			}
			if sym.GridFirstSpacing <= 0 {
				return fmt.Errorf("symbols[%d]: grid_first_spacing 必须 > 0", i)
			}
			if sym.GridSpacingMultiplier <= 1.0 {
				return fmt.Errorf("symbols[%d]: grid_spacing_multiplier 必须 > 1.0 (几何增长)", i)
			}
			if sym.GridMaxSpacing > 0 && sym.GridMaxSpacing < sym.GridFirstSpacing {
				return fmt.Errorf("symbols[%d]: grid_max_spacing 必须 >= grid_first_spacing", i)
			}
		}

		// 兼容旧配置的验证（如果新配置未设置）
		if sym.GridStartOffset == 0 && sym.GridFirstSpacing == 0 {
			if sym.NearLayers < 1 || sym.NearLayers > 20 {
				return fmt.Errorf("symbols[%d]: near_layers 必须在 1-20 之间", i)
			}
			if sym.FarLayers < 0 || sym.FarLayers > 30 {
				return fmt.Errorf("symbols[%d]: far_layers 必须在 0-30 之间", i)
			}
		}

		if sym.MaxCancelPerMin <= 0 || sym.MaxCancelPerMin > 300 {
			return fmt.Errorf("symbols[%d]: max_cancel_per_min 必须在 (0, 300] 之间", i)
		}

		// 验证模式阈值的合理性
		if sym.PinningEnabled && sym.GrindingEnabled {
			if sym.PinningThresh >= sym.GrindingThresh {
				return fmt.Errorf("symbols[%d]: pinning_thresh (%.2f) 必须 < grinding_thresh (%.2f)，确保Grinding优先级高于Pinning",
					i, sym.PinningThresh, sym.GrindingThresh)
			}
		}

		// 验证阈值范围
		if sym.PinningThresh > 0 && (sym.PinningThresh < 0.5 || sym.PinningThresh > 0.95) {
			return fmt.Errorf("symbols[%d]: pinning_thresh 必须在 [0.5, 0.95] 之间", i)
		}
		if sym.GrindingThresh > 0 && (sym.GrindingThresh < 0.5 || sym.GrindingThresh > 0.98) {
			return fmt.Errorf("symbols[%d]: grinding_thresh 必须在 [0.5, 0.98] 之间", i)
		}

		// 验证止损阈值
		if sym.StopLossThresh > 0 && (sym.StopLossThresh < 0.05 || sym.StopLossThresh > 0.5) {
			return fmt.Errorf("symbols[%d]: stop_loss_thresh 必须在 [0.05, 0.5] 之间", i)
		}

		// 回写到配置中（确保兼容性转换生效）
		cfg.Symbols[i] = *sym
	}

	return nil
}

// watchConfig 监听配置文件变化并热重载
func watchConfig() {
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Info().Str("file", e.Name).Msg("检测到配置文件变化，正在重载...")

		var newCfg Config
		if err := viper.Unmarshal(&newCfg); err != nil {
			log.Error().Err(err).Msg("重载配置失败")
			return
		}

		if err := validateConfig(&newCfg); err != nil {
			log.Error().Err(err).Msg("新配置验证失败，保持旧配置")
			return
		}

		globalConfig = &newCfg
		log.Info().Msg("配置热重载成功")
	})
}

// GetQuoteInterval 获取报价间隔
func (c *Config) GetQuoteInterval() time.Duration {
	return time.Duration(c.Global.QuoteIntervalMs) * time.Millisecond
}

// GetSymbolConfig 根据交易对符号获取配置
func (c *Config) GetSymbolConfig(symbol string) *SymbolConfig {
	for i := range c.Symbols {
		if c.Symbols[i].Symbol == symbol {
			return &c.Symbols[i]
		}
	}
	return nil
}

// GetAllSymbols 获取所有交易对符号列表
func (c *Config) GetAllSymbols() []string {
	symbols := make([]string, len(c.Symbols))
	for i, sym := range c.Symbols {
		symbols[i] = sym.Symbol
	}
	return symbols
}
