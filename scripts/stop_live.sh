#!/usr/bin/env bash
# Phoenix v2: 实盘停止脚本
# 用法: ./stop_live.sh

set -e

PID_FILE="./run/phoenix_live.pid"
CONFIG_FILE="./configs/phoenix_live.yaml"
LOG_DIR="./logs"

echo "==================== Phoenix实盘停止 ===================="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"

# 确保使用正确的配置文件
if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "警告: 配置文件 $CONFIG_FILE 不存在，尝试使用默认配置"
    CONFIG_FILE="./config.yaml"
fi

echo "使用配置文件: $CONFIG_FILE"

# 【关键修复】先执行紧急撤单，防止在kill进程窗口内产生新订单
echo ""
echo ">>> 步骤1: 执行紧急撤单和平仓（在停止进程之前）..."
if go run cmd/emergency/main.go -config "$CONFIG_FILE" 2>&1 | tee -a "$LOG_DIR/emergency_stop.log"; then
    echo "紧急撤单执行完成"
else
    echo "警告: 紧急撤单命令返回非零状态，继续执行停止流程..."
fi

echo ""
echo ">>> 步骤2: 停止主进程..."

# 检查PID文件是否存在
if [[ -f "$PID_FILE" ]]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        echo "正在停止进程 $PID..."
        
        # 发送SIGTERM让进程优雅退出
        kill "$PID"
        
        # 等待进程结束
        TIMEOUT=30
        COUNT=0
        while kill -0 "$PID" 2>/dev/null && [[ $COUNT -lt $TIMEOUT ]]; do
            sleep 1
            ((COUNT++))
            if (( COUNT % 5 == 0 )); then
                echo "等待进程退出... ($COUNT/$TIMEOUT秒)"
            fi
        done
        
        if kill -0 "$PID" 2>/dev/null; then
            echo "进程未正常退出，强制终止..."
            kill -9 "$PID"
            sleep 1
        else
            echo "进程已正常退出"
        fi
    else
        echo "进程 $PID 不存在"
    fi
    rm -f "$PID_FILE"
else
    echo "PID文件不存在，尝试查找相关进程..."
fi

# 查找并清理所有相关的phoenix进程
echo ""
echo ">>> 步骤3: 清理所有残留进程..."

# 查找所有phoenix相关进程
PIDS=$(pgrep -f "phoenix" 2>/dev/null || true)
if [[ -n "$PIDS" ]]; then
    echo "找到相关进程: $PIDS"
    for pid in $PIDS; do
        # 跳过当前脚本自身
        if [[ "$pid" != "$$" ]]; then
            echo "终止进程 $pid..."
            kill "$pid" 2>/dev/null || true
        fi
    done
    sleep 2
    
    # 强制终止仍在运行的进程
    REMAINING=$(pgrep -f "phoenix" 2>/dev/null || true)
    if [[ -n "$REMAINING" ]]; then
        echo "强制终止残留进程: $REMAINING"
        for pid in $REMAINING; do
            if [[ "$pid" != "$$" ]]; then
                kill -9 "$pid" 2>/dev/null || true
            fi
        done
    fi
else
    echo "未找到相关进程"
fi

# 【关键修复】再次执行撤单，确保进程停止后没有遗留订单
echo ""
echo ">>> 步骤4: 再次执行撤单确认（确保无遗留订单）..."
if go run cmd/emergency/main.go -config "$CONFIG_FILE" 2>&1 | tee -a "$LOG_DIR/emergency_stop.log"; then
    echo "二次撤单确认完成"
else
    echo "警告: 二次撤单命令返回非零状态"
fi

# 清理锁文件
echo ""
echo ">>> 步骤5: 清理锁文件..."
rm -f /tmp/phoenix_runner.lock
rm -f /tmp/phoenix_*.lock
echo "锁文件已清理"

echo ""
echo "==================== Phoenix实盘已停止 ===================="
echo "完成时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""
echo "建议: 使用以下命令确认交易所上没有残留订单:"
echo "  curl -s 'https://fapi.binance.com/fapi/v1/openOrders' (需要签名)"
echo ""
