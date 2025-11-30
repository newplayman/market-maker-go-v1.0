#!/usr/bin/env bash
# Phoenix v2: 实盘启动脚本
# 用法: ./start_live.sh
# 依赖: Go 1.21+

set -e  # 错误退出

# 检查环境变量
if [[ -z "$BINANCE_API_KEY" || -z "$BINANCE_API_SECRET" ]]; then
    echo "错误: 请设置BINANCE_API_KEY和BINANCE_API_SECRET环境变量"
    exit 1
fi

# 默认env
CONFIG="configs/phoenix_live.yaml"
LOG_DIR="./logs"
METRICS_PORT=8080
SNAPSHOT_DIR="./data"
RUN_DIR="./run"

echo "==================== Phoenix实盘启动 ===================="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"

# 检查config有效
if [[ ! -f "$CONFIG" ]]; then
    echo "错误: 配置 $CONFIG 不存在"
    exit 1
fi

# 创建必要目录
mkdir -p "$LOG_DIR" "$SNAPSHOT_DIR" "$RUN_DIR"

# 【关键修复】检查是否已有进程在运行
echo ""
echo ">>> 步骤1: 检查现有进程..."
if [[ -f "$RUN_DIR/phoenix_live.pid" ]]; then
    OLD_PID=$(cat "$RUN_DIR/phoenix_live.pid")
    if kill -0 "$OLD_PID" 2>/dev/null; then
        echo "警告: 发现已运行的Phoenix进程 (PID: $OLD_PID)"
        echo "请先运行 ./scripts/stop_live.sh 停止现有进程"
        exit 1
    else
        echo "清理过期的PID文件..."
        rm -f "$RUN_DIR/phoenix_live.pid"
    fi
fi

# 清理锁文件
rm -f /tmp/phoenix_runner.lock
rm -f /tmp/phoenix_*.lock

# 查找其他phoenix进程
EXISTING_PIDS=$(pgrep -f "phoenix" 2>/dev/null || true)
if [[ -n "$EXISTING_PIDS" ]]; then
    echo "警告: 发现其他Phoenix相关进程: $EXISTING_PIDS"
    echo "建议先运行 ./scripts/stop_live.sh 确保清理干净"
    read -p "是否继续启动? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# 【关键修复】启动前清理交易所上的遗留订单
echo ""
echo ">>> 步骤2: 清理交易所上的遗留订单..."
echo "使用配置文件: $CONFIG"
if go run cmd/emergency/main.go -config "$CONFIG" 2>&1 | tee -a "$LOG_DIR/startup_cleanup.log"; then
    echo "遗留订单清理完成"
else
    echo "警告: 清理命令返回非零状态，可能没有遗留订单"
fi

# 等待清理完成
sleep 2

# 构建主程序
echo ""
echo ">>> 步骤3: 构建程序..."
make build

# 启动应用
echo ""
echo ">>> 步骤4: 启动应用..."
echo "配置文件: $CONFIG"
nohup ./bin/phoenix -config="$CONFIG" -log=info > "$LOG_DIR/phoenix_live.out" 2>&1 &
PID=$!
echo "$PID" > "$RUN_DIR/phoenix_live.pid"

# 等待启动
echo "等待应用初始化..."
sleep 5

if kill -0 "$PID" 2>/dev/null; then
    echo ""
    echo "==================== 启动成功 ===================="
    echo "PID: $PID"
    echo "配置: $CONFIG"
    echo "日志: $LOG_DIR/phoenix_live.out"
    echo "快照: $SNAPSHOT_DIR"
    echo ""
    echo "监控命令:"
    echo "  查看日志: tail -f $LOG_DIR/phoenix_live.out"
    echo "  查看指标: curl http://localhost:$METRICS_PORT/metrics | grep mm_"
    echo "  停止服务: ./scripts/stop_live.sh"
    echo ""
    echo "【注意】不再启动后台健康检查进程，请使用systemd或supervisor管理服务"
    echo "========================================================"
else
    echo ""
    echo "==================== 启动失败 ===================="
    echo "请检查日志: cat $LOG_DIR/phoenix_live.out"
    rm -f "$RUN_DIR/phoenix_live.pid"
    exit 1
fi
