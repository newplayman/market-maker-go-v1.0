#!/usr/bin/env bash
set -euo pipefail

# Phoenix v2 紧急刹车脚本：撤单 + 平仓（Reduce-Only 市价）
# 使用：在生产VPS上执行本脚本，确保 BINANCE_API_KEY / BINANCE_API_SECRET 已经在环境中设置。

: "${BINANCE_API_KEY:?请在环境中设置 BINANCE_API_KEY}"
: "${BINANCE_API_SECRET:?请在环境中设置 BINANCE_API_SECRET}"

# 可选：自定义REST端点（生产/测试网）
export BINANCE_REST_URL="${BINANCE_REST_URL:-https://fapi.binance.com}"

# 构建应急工具
make build-emergency || go build -o bin/emergency cmd/emergency/main.go

# 执行应急平仓
./bin/emergency -config "${PHOENIX_CONFIG_PATH:-config.yaml}" -log warn

echo "✅ 紧急刹车完成：已尝试撤销所有挂单并按仓位方向使用Reduce-Only市价单平仓。"
