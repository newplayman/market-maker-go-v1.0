#!/bin/bash
# 流量监控脚本 - 300ms优化测试

PID_FILE="./logs/phoenix_live.pid"
LOG_FILE="./logs/phoenix_live.out"
REPORT_FILE="./logs/traffic_report_300ms.txt"

if [ ! -f "$PID_FILE" ]; then
    echo "错误: 找不到PID文件 $PID_FILE"
    exit 1
fi

PID=$(cat "$PID_FILE")

if ! kill -0 "$PID" 2>/dev/null; then
    echo "错误: 进程 $PID 不存在"
    exit 1
fi

echo "==================== 流量监控报告 ===================="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "进程PID: $PID"
echo "配置文件: configs/phoenix_live.yaml"
echo ""

# 1. 检查WebSocket连接
echo ">>> 1. WebSocket连接状态:"
if ss -tnp 2>/dev/null | grep "$PID" | grep -q ESTAB; then
    CONN_COUNT=$(ss -tnp 2>/dev/null | grep "$PID" | grep ESTAB | wc -l)
    echo "  ✅ 已建立 $CONN_COUNT 个连接"
    ss -tnp 2>/dev/null | grep "$PID" | grep ESTAB | head -3 | while read line; do
        echo "  $line"
    done
else
    echo "  ⚠️  未找到活跃连接"
fi
echo ""

# 2. 检查压缩是否启用
echo ">>> 2. WebSocket压缩状态:"
if grep -q "WebSocket压缩协商" "$LOG_FILE"; then
    COMPRESS=$(grep "WebSocket压缩协商" "$LOG_FILE" | tail -1)
    echo "  ✅ $COMPRESS"
else
    echo "  ⚠️  未找到压缩协商信息"
fi
echo ""

# 3. 检查订阅的stream格式
echo ">>> 3. 订阅的Stream格式:"
if grep -q "depth20@300ms" "$LOG_FILE" 2>/dev/null; then
    echo "  ✅ 已确认使用 @depth20@300ms"
elif grep -q "@depth" "$LOG_FILE" 2>/dev/null; then
    STREAM=$(grep "@depth" "$LOG_FILE" | tail -1)
    echo "  ⚠️  找到: $STREAM"
else
    echo "  ⚠️  未找到stream信息"
fi
echo ""

# 4. 检查深度数据更新
echo ">>> 4. 深度数据更新状态 (最近1分钟):"
RECENT_UPDATES=$(tail -1000 "$LOG_FILE" | grep -E "(OnDepth|价格更新|mid_price)" | grep -v "告警\|严重" | tail -5)
if [ -n "$RECENT_UPDATES" ]; then
    echo "  ✅ 有深度数据更新:"
    echo "$RECENT_UPDATES" | head -3 | sed 's/^/    /'
else
    echo "  ⚠️  最近1分钟内无深度数据更新"
    echo "  (这可能是正常的，如果刚启动)"
fi
echo ""

# 5. 网络流量统计（使用/proc）
echo ">>> 5. 网络流量统计 (来自/proc/net/sockstat):"
if [ -f "/proc/$PID/net/sockstat" ]; then
    cat "/proc/$PID/net/sockstat" | grep -E "(TCP|sockets)"
else
    echo "  ⚠️  无法读取网络统计"
fi
echo ""

# 6. 进程资源使用
echo ">>> 6. 进程资源使用:"
if ps -p "$PID" -o pid,pcpu,pmem,rss,vsz,etime,cmd --no-headers 2>/dev/null; then
    :
else
    echo "  ⚠️  无法获取进程信息"
fi
echo ""

# 7. 最近日志摘要
echo ">>> 7. 最近日志摘要 (最后10行):"
tail -10 "$LOG_FILE" | grep -v "严重告警\|告警\|TICKER_EVENT\|风控指标\|同步活跃订单" | head -5 | sed 's/^/    /'
echo ""

echo "==================== 监控完成 ===================="
echo ""

# 保存报告
{
    echo "流量监控报告 - $(date '+%Y-%m-%d %H:%M:%S')"
    echo "PID: $PID"
    echo ""
    echo "WebSocket连接数: $CONN_COUNT"
    echo "压缩状态: $(grep "WebSocket压缩协商" "$LOG_FILE" | tail -1 | cut -d: -f2-)"
    echo ""
} > "$REPORT_FILE"

echo "报告已保存到: $REPORT_FILE"

