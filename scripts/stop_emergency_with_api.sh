#!/usr/bin/env bash
# Phoenix v2: 一键紧急刹车 - 杀进程+撤单+平仓
# 用法: ./stop_emergency_with_api.sh [API_KEY] [API_SECRET]

set -e

# 获取API密钥（从参数或环境变量）
API_KEY=${1:-$BINANCE_API_KEY}
API_SECRET=${2:-$BINANCE_API_SECRET}

if [[ -z "$API_KEY" || -z "$API_SECRET" ]]; then
    echo "错误: 需提供API_KEY和API_SECRET (env或参数)"
    exit 1
fi

# 设置环境变量供emergency程序使用
export BINANCE_API_KEY="$API_KEY"
export BINANCE_API_SECRET="$API_SECRET"

LOG_DIR="./logs"
EMERG_LOG="$LOG_DIR/emergency_stop_$(date +%Y%m%d_%H%M%S).log"

echo "紧急刹车启动: $(date)" | tee -a "$EMERG_LOG"

# Step 1: 停止主程序进程
if [[ -f run/phoenix_test.pid ]]; then
    PID=$(cat run/phoenix_test.pid)
    echo "尝试终止PID: $PID" | tee -a "$EMERG_LOG"
    if kill -0 "$PID" 2>/dev/null; then
        # 先尝试优雅关闭
        kill -TERM "$PID" 2>/dev/null || true
        sleep 5
        
        # 如果进程仍然存在，则强制终止
        if kill -0 "$PID" 2>/dev/null; then
            echo "进程未正常退出，强制终止..." | tee -a "$EMERG_LOG"
            kill -9 "$PID" 2>/dev/null || true
        fi
        
        echo "主程序进程(PID: $PID)已停止" | tee -a "$EMERG_LOG"
    else
        echo "PID文件存在但进程未运行" | tee -a "$EMERG_LOG"
    fi
    rm -f run/phoenix_test.pid
else
    echo "未找到PID文件，尝试杀死所有phoenix进程" | tee -a "$EMERG_LOG"
    pkill -TERM -f "phoenix" || true
    sleep 3
    pkill -9 -f "phoenix" || true
fi

# Step 2: 构建并执行应急工具
echo "构建并执行紧急刹车工具..." | tee -a "$EMERG_LOG"
make build-emergency || go build -o bin/emergency cmd/emergency/main.go

# Step 3: 执行应急平仓
PHOENIX_CONFIG_PATH="configs/phoenix_test_190.yaml" ./bin/emergency -config "configs/phoenix_test_190.yaml" -log warn 2>&1 | tee -a "$EMERG_LOG"

echo "刹车完成! 检查仓位: https://www.binance.com/en/futures/ETHUSDC" | tee -a "$EMERG_LOG"
echo "紧急日志: $EMERG_LOG" | tee -a "$EMERG_LOG"