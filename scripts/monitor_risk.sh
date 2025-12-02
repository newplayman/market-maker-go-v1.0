#!/bin/bash

# Phoenix风控实时监控脚本

echo "================================================"
echo "Phoenix 风控实时监控"
echo "================================================"
echo ""

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

LOG_FILE="logs/phoenix_live.out"

if [ ! -f "$LOG_FILE" ]; then
    echo -e "${RED}错误：日志文件不存在${NC}"
    exit 1
fi

# 获取最新状态
echo -e "${BLUE}【当前状态】${NC}"
echo "-----------------------------------"

# 持仓信息
latest_pos=$(grep "风控指标" "$LOG_FILE" | tail -1)
if [ -n "$latest_pos" ]; then
    echo "$latest_pos" | sed 's/.*INF //'
else
    echo "暂无持仓数据"
fi

echo ""

# 活跃订单
latest_orders=$(grep "同步活跃订单" "$LOG_FILE" | tail -1)
if [ -n "$latest_orders" ]; then
    echo "$latest_orders" | sed 's/.*INF //'
else
    echo "暂无订单数据"
fi

echo ""
echo -e "${BLUE}【风控事件统计】${NC}"
echo "-----------------------------------"

# 统计各类事件
grinding_count=$(grep -c "触发Grinding" "$LOG_FILE" 2>/dev/null || echo 0)
warning_count=$(grep -c "风控警告" "$LOG_FILE" 2>/dev/null || echo 0)
circuit_count=$(grep -c "紧急熔断" "$LOG_FILE" 2>/dev/null || echo 0)
exceed_count=$(grep -c "持仓已超" "$LOG_FILE" 2>/dev/null || echo 0)
adjust_count=$(grep -c "根据风控要求调整报价" "$LOG_FILE" 2>/dev/null || echo 0)

echo "Grinding触发次数: $grinding_count"
echo "50%持仓警告次数: $warning_count"
echo "80%紧急熔断次数: $circuit_count"
echo "持仓超标拦截次数: $exceed_count"
echo "风控调整层数次数: $adjust_count"

echo ""
echo -e "${BLUE}【最近的重要事件】${NC}"
echo "-----------------------------------"

# 显示最近的关键事件
echo -e "${YELLOW}最近的风控调整：${NC}"
grep "根据风控要求调整报价" "$LOG_FILE" | tail -3 | sed 's/.*INF //' || echo "无"

echo ""
echo -e "${YELLOW}Grinding模式：${NC}"
grep "Grinding" "$LOG_FILE" | tail -3 | sed 's/.*[A-Z] //' || echo "未触发"

echo ""
echo -e "${YELLOW}风控警告：${NC}"
grep -E "风控警告|持仓已超|紧急熔断" "$LOG_FILE" | tail -3 | sed 's/.*[A-Z] //' || echo "无"

echo ""
echo "================================================"
echo -e "${GREEN}持续监控中...${NC}"
echo "按 Ctrl+C 退出"
echo "================================================"
echo ""

# 实时监控模式
if [ "$1" == "-f" ] || [ "$1" == "--follow" ]; then
    tail -f "$LOG_FILE" | grep --line-buffered -E "风控|Grinding|熔断|警告|持仓已超|pos=|pending_buy=|FILL"
fi

