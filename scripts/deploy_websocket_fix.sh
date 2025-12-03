#!/bin/bash

# WebSocket断流修复和流量优化 - 部署脚本
# 用途：安全地重启服务以应用WebSocket修复

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

LOG_FILE="logs/phoenix_live.out"
PID_FILE="logs/phoenix_live.pid"
CONFIG_FILE="configs/phoenix_live.yaml"

echo "=========================================="
echo "WebSocket断流修复和流量优化 - 部署脚本"
echo "=========================================="
echo ""

# 1. 检查当前进程
if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE" 2>/dev/null || echo "")
    if [ -n "$OLD_PID" ] && ps -p "$OLD_PID" > /dev/null 2>&1; then
        echo "✓ 发现运行中的进程 (PID: $OLD_PID)"
        echo "  准备优雅停止..."
        kill -TERM "$OLD_PID" 2>/dev/null || true
        sleep 3
        
        # 如果还在运行，强制停止
        if ps -p "$OLD_PID" > /dev/null 2>&1; then
            echo "  强制停止..."
            kill -9 "$OLD_PID" 2>/dev/null || true
            sleep 2
        fi
        echo "✓ 旧进程已停止"
    else
        echo "✓ 未发现运行中的进程"
    fi
else
    echo "✓ 未发现PID文件，可能进程未运行"
fi

# 2. 验证配置文件
if [ ! -f "$CONFIG_FILE" ]; then
    echo "✗ 错误: 配置文件不存在: $CONFIG_FILE"
    exit 1
fi
echo "✓ 配置文件存在: $CONFIG_FILE"

# 3. 验证可执行文件
if [ ! -f "bin/phoenix" ]; then
    echo "✗ 错误: 可执行文件不存在，请先运行: make build"
    exit 1
fi
echo "✓ 可执行文件存在: bin/phoenix"

# 4. 创建必要的目录
mkdir -p logs data
echo "✓ 目录已创建"

# 5. 启动新进程
echo ""
echo "启动新进程..."
nohup ./bin/phoenix --config "$CONFIG_FILE" > "$LOG_FILE" 2>&1 &
NEW_PID=$!
echo "$NEW_PID" > "$PID_FILE"
echo "✓ 新进程已启动 (PID: $NEW_PID)"
echo ""

# 6. 等待进程稳定
echo "等待进程稳定（5秒）..."
sleep 5

# 7. 检查进程是否还在运行
if ps -p "$NEW_PID" > /dev/null 2>&1; then
    echo "✓ 进程运行正常 (PID: $NEW_PID)"
else
    echo "✗ 错误: 进程启动失败"
    echo "查看日志: tail -50 $LOG_FILE"
    exit 1
fi

# 8. 显示关键日志
echo ""
echo "=========================================="
echo "关键日志（最近20行）:"
echo "=========================================="
tail -20 "$LOG_FILE" | grep -E "(启动|连接|WebSocket|压缩|ERR|告警)" || tail -20 "$LOG_FILE"
echo ""

# 9. 提示监控命令
echo "=========================================="
echo "监控命令:"
echo "=========================================="
echo "  查看实时日志: tail -f $LOG_FILE"
echo "  查看WebSocket相关: tail -f $LOG_FILE | grep -E '(WebSocket|重连|压缩|断流)'"
echo "  查看错误: tail -f $LOG_FILE | grep ERR"
echo "  监控流量: ./scripts/monitor_traffic.sh"
echo "  检查进程: ps aux | grep phoenix"
echo ""

echo "=========================================="
echo "部署完成！"
echo "=========================================="
echo ""
echo "【重要】请观察以下指标："
echo "  1. WebSocket是否正常连接（查看日志中的'WebSocket连接成功'）"
echo "  2. 压缩是否生效（查看日志中的'WebSocket压缩协商'）"
echo "  3. 价格数据是否持续更新（不应出现'价格数据过期'告警）"
echo "  4. 流量是否降低（使用monitor_traffic.sh监控）"
echo ""
echo "如果流量仍>400k，可切换到3交易对配置："
echo "  ./bin/phoenix --config configs/phoenix_live_3symbols.yaml"
echo ""


