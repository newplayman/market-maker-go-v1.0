#!/usr/bin/env bash
set -euo pipefail

# Phoenix v2 一键启动（生产）
# 要求：VPS已经将本机公网IP加入币安API白名单；已安装 Go 1.21+。
# 使用：填写环境变量后直接执行本脚本。

# 必填：API密钥（建议从VPS环境变量注入，不写入配置文件）
: "${BINANCE_API_KEY:?请在环境中设置 BINANCE_API_KEY}"
: "${BINANCE_API_SECRET:?请在环境中设置 BINANCE_API_SECRET}"

# 可选：自定义REST/WS端点（默认为生产）
export BINANCE_REST_URL="${BINANCE_REST_URL:-https://fapi.binance.com}"
export BINANCE_WS_ENDPOINT="${BINANCE_WS_ENDPOINT:-wss://fstream.binance.com}"

# 目录准备
mkdir -p bin logs run data

# 构建主程序
make build

# 启动Prometheus端口在配置文件global.metrics_port（默认9090）
# 前台启动请去掉 nohup 与 &
nohup ./bin/phoenix -config "${PHOENIX_CONFIG_PATH:-config.yaml}" -log "${PHOENIX_LOG_LEVEL:-info}" \
  > logs/phoenix.out 2>&1 &
PID=$!
echo "$PID" > run/phoenix.pid

echo "Phoenix 已启动，PID=${PID}"
echo "日志：logs/phoenix.out"
echo "Metrics：http://$(hostname -I | awk '{print $1}'):${PHOENIX_METRICS_PORT:-9090}/metrics"
