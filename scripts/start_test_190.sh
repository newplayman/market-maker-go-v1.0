#!/usr/bin/env bash
# Phoenix v2: 一键启动190 USDC ETHUSDC实盘测试
# 用法: ./start_test_190.sh
# 依赖: Go 1.21+

set -e  # 错误退出

# 默认env
CONFIG="configs/phoenix_test_190.yaml"
LOG_DIR="./logs"
METRICS_PORT=8080
SNAPSHOT_DIR="./data"

# 检查config有效
if [[ ! -f "$CONFIG" ]]; then
    echo "错误: 配置 $CONFIG 不存在"
    exit 1
fi

echo "启动Phoenix测试: 190 USDC ETHUSDC, config=$CONFIG"

# 构建主程序
make build

# 启动应用
nohup ./bin/phoenix -config="$CONFIG" -log=info > logs/phoenix_test.out 2>&1 &
PID=$!
echo "$PID" > run/phoenix_test.pid

# 等待启动
sleep 5

if kill -0 "$PID" 2>/dev/null; then
    echo "成功启动! Metrics: http://localhost:$METRICS_PORT"
    echo "日志: $LOG_DIR ; 快照: $SNAPSHOT_DIR"
    echo "监控: curl http://localhost:$METRICS_PORT/metrics | grep mm_"
    
    # 后台运行健康检查，避免阻塞脚本
    {
        while true; do
            sleep 30
            if [[ -f run/phoenix_test.pid ]]; then
                RUNNING_PID=$(cat run/phoenix_test.pid)
                if ! kill -0 "$RUNNING_PID" 2>/dev/null; then
                    echo "告警: 应用已停止! 运行 ./scripts/stop_emergency.sh"
                    rm -f run/phoenix_test.pid
                    break
                fi
            else
                break
            fi
        done
    } &
    
    echo "健康检查已在后台运行"
else
    echo "启动失败: 检查日志 cat logs/phoenix_test.out"
    rm -f run/phoenix_test.pid
    exit 1
fi