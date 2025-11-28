package gateway

import (
	"os"
	"strings"
)

// EnvConfig 从环境变量构造 Binance 客户端。
type EnvConfig struct {
	APIKey     string
	APISecret  string
	RestURL    string
	WSEndpoint string
}

// LoadEnvConfig 读取 API 密钥与端点（可选），若未设置则使用默认。
func LoadEnvConfig() EnvConfig {
	return EnvConfig{
		APIKey:     os.Getenv("BINANCE_API_KEY"),
		APISecret:  os.Getenv("BINANCE_API_SECRET"),
		RestURL:    pick(os.Getenv("BINANCE_REST_URL"), BinanceFuturesRestEndpoint),
		WSEndpoint: pick(os.Getenv("BINANCE_WS_ENDPOINT"), BinanceFuturesWSEndpoint),
	}
}

func pick(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
