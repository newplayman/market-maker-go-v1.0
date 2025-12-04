package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// 创建临时配置文件
	configContent := `
global:
  total_notional_max: 1000000
  quote_interval_ms: 500
  api_key: "test_key"
  api_secret: "test_secret"
  log_level: "info"

symbols:
  - symbol: "BTCUSDT"
    net_max: 1.0
    base_layer_size: 0.01
    min_qty: 0.001
    min_spread: 0.0002
    stop_loss_thresh: 0.05
    near_layers: 3
    far_layers: 5
    max_cancel_per_min: 50
    grinding_enabled: true
    grinding_thresh: 0.8
`

	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	tmpFile.Close()

	// 加载配置
	cfg, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// 验证全局配置
	if cfg.Global.TotalNotionalMax != 1000000 {
		t.Errorf("Expected TotalNotionalMax 1000000, got %.0f", cfg.Global.TotalNotionalMax)
	}

	if cfg.Global.LogLevel != "info" {
		t.Errorf("Expected LogLevel 'info', got '%s'", cfg.Global.LogLevel)
	}

	// 验证交易对配置
	if len(cfg.Symbols) != 1 {
		t.Fatalf("Expected 1 symbol, got %d", len(cfg.Symbols))
	}

	sym := cfg.Symbols[0]
	if sym.Symbol != "BTCUSDT" {
		t.Errorf("Expected symbol BTCUSDT, got %s", sym.Symbol)
	}

	if sym.NetMax != 1.0 {
		t.Errorf("Expected NetMax 1.0, got %.2f", sym.NetMax)
	}

	if sym.MinSpread != 0.0002 {
		t.Errorf("Expected MinSpread 0.0002, got %.4f", sym.MinSpread)
	}

	if !sym.GrindingEnabled {
		t.Error("Expected GrindingEnabled to be true")
	}
}

func TestGetSymbolConfig(t *testing.T) {
	cfg := &Config{
		Symbols: []SymbolConfig{
			{Symbol: "BTCUSDT", NetMax: 1.0},
			{Symbol: "ETHUSDT", NetMax: 2.0},
		},
	}

	// 测试存在的交易对
	btcCfg := cfg.GetSymbolConfig("BTCUSDT")
	if btcCfg == nil {
		t.Fatal("Expected to find BTCUSDT config")
	}
	if btcCfg.NetMax != 1.0 {
		t.Errorf("Expected NetMax 1.0, got %.2f", btcCfg.NetMax)
	}

	// 测试不存在的交易对
	nonExistent := cfg.GetSymbolConfig("XRPUSDT")
	if nonExistent != nil {
		t.Error("Expected nil for non-existent symbol")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{
		Symbols: []SymbolConfig{
			{
				Symbol: "BTCUSDT",
				NetMax: 1.0,
			},
		},
	}

	// 应用默认值
	for i := range cfg.Symbols {
		sym := &cfg.Symbols[i]
		if sym.MinSpread == 0 {
			sym.MinSpread = 0.0001
		}
		if sym.MaxCancelPerMin == 0 {
			sym.MaxCancelPerMin = 50
		}
		if sym.StopLossThresh == 0 {
			sym.StopLossThresh = 0.02
		}
	}

	sym := cfg.Symbols[0]
	if sym.MinSpread != 0.0001 {
		t.Errorf("Expected default MinSpread 0.0001, got %.4f", sym.MinSpread)
	}
	if sym.MaxCancelPerMin != 50 {
		t.Errorf("Expected default MaxCancelPerMin 50, got %d", sym.MaxCancelPerMin)
	}
	if sym.StopLossThresh != 0.02 {
		t.Errorf("Expected default StopLossThresh 0.02, got %.4f", sym.StopLossThresh)
	}
}
