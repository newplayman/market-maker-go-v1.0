#!/bin/bash

# Phoenix做市商健康检查脚本
# 用途：检测进程是否处于"假死"状态，及时发现异常并告警

set -e

# 配置
LOG_FILE="logs/phoenix_live.out"
PID_FILE="run/phoenix_live.pid"
CONFIG_FILE="configs/phoenix_live.yaml"
SYMBOL="ETHUSDC"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查结果
HEALTH_STATUS=0  # 0=健康, 1=警告, 2=严重

echo "================================================"
echo "Phoenix 做市商健康检查"
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "================================================"
echo ""

# 检查1: 进程是否运行
echo -e "${YELLOW}[检查1] 进程状态${NC}"
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if ps -p "$PID" > /dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} 进程运行中 (PID: $PID)"
    else
        echo -e "${RED}✗${NC} 进程已停止 (PID文件存在但进程不存在)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${RED}✗${NC} PID文件不存在"
    HEALTH_STATUS=2
fi
echo ""

# 检查2: 日志文件是否更新
echo -e "${YELLOW}[检查2] 日志活跃度${NC}"
if [ -f "$LOG_FILE" ]; then
    LOG_AGE=$(stat -c %Y "$LOG_FILE")
    CURRENT_TIME=$(date +%s)
    LOG_STALE=$((CURRENT_TIME - LOG_AGE))
    
    if [ "$LOG_STALE" -lt 10 ]; then
        echo -e "${GREEN}✓${NC} 日志正常更新 (${LOG_STALE}秒前)"
    elif [ "$LOG_STALE" -lt 30 ]; then
        echo -e "${YELLOW}⚠${NC} 日志更新较慢 (${LOG_STALE}秒前)"
        [ "$HEALTH_STATUS" -lt 1 ] && HEALTH_STATUS=1
    else
        echo -e "${RED}✗${NC} 日志长时间未更新 (${LOG_STALE}秒前)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${RED}✗${NC} 日志文件不存在"
    HEALTH_STATUS=2
fi
echo ""

# 检查3: 价格数据更新（检查深度更新或价格相关日志）
echo -e "${YELLOW}[检查3] 价格数据新鲜度${NC}"
if [ -f "$LOG_FILE" ]; then
    # 检查最近60秒内是否有报价生成日志（间接证明价格在更新）
    RECENT_QUOTE_COUNT=$(tail -n 500 "$LOG_FILE" | grep -c "报价已生成" || echo 0)
    
    if [ "$RECENT_QUOTE_COUNT" -gt 10 ]; then
        echo -e "${GREEN}✓${NC} 价格数据正常 (最近有${RECENT_QUOTE_COUNT}次报价生成)"
    elif [ "$RECENT_QUOTE_COUNT" -gt 0 ]; then
        echo -e "${YELLOW}⚠${NC} 价格数据更新较少 (最近仅${RECENT_QUOTE_COUNT}次报价)"
        [ "$HEALTH_STATUS" -lt 1 ] && HEALTH_STATUS=1
    else
        echo -e "${RED}✗${NC} 价格数据停止更新 (无报价生成)"
        HEALTH_STATUS=2
    fi
    
    # 检查是否有价格过期告警
    STALE_PRICE_WARN=$(tail -n 100 "$LOG_FILE" 2>/dev/null | grep -c "价格数据过期" 2>/dev/null || echo "0")
    STALE_PRICE_WARN=$(echo "$STALE_PRICE_WARN" | tr -d '\n\r' | xargs)
    if [ "$STALE_PRICE_WARN" != "0" ] && [ "$STALE_PRICE_WARN" -gt 0 ] 2>/dev/null; then
        echo -e "${RED}✗${NC} 发现价格过期告警 (${STALE_PRICE_WARN}次)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${RED}✗${NC} 无法检查价格数据"
    HEALTH_STATUS=2
fi
echo ""

# 检查4: 订单操作活跃度
echo -e "${YELLOW}[检查4] 订单活跃度${NC}"
if [ -f "$LOG_FILE" ]; then
    # 检查最近120秒内的订单操作
    RECENT_ORDER_OPS=$(tail -n 500 "$LOG_FILE" | grep -c "订单已下达\|订单已撤销" || echo 0)
    
    if [ "$RECENT_ORDER_OPS" -gt 5 ]; then
        echo -e "${GREEN}✓${NC} 订单操作正常 (最近${RECENT_ORDER_OPS}次操作)"
    elif [ "$RECENT_ORDER_OPS" -gt 0 ]; then
        echo -e "${YELLOW}⚠${NC} 订单操作较少 (最近${RECENT_ORDER_OPS}次操作)"
        [ "$HEALTH_STATUS" -lt 1 ] && HEALTH_STATUS=1
    else
        echo -e "${RED}✗${NC} 订单长时间未更新 (可能假死)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${RED}✗${NC} 无法检查订单活跃度"
    HEALTH_STATUS=2
fi
echo ""

# 检查5: 错误和告警
echo -e "${YELLOW}[检查5] 错误和告警${NC}"
if [ -f "$LOG_FILE" ]; then
    # 检查最近的ERROR日志
    RECENT_ERRORS=$(tail -n 200 "$LOG_FILE" | grep -c " ERR " || echo 0)
    
    if [ "$RECENT_ERRORS" -eq 0 ]; then
        echo -e "${GREEN}✓${NC} 无错误日志"
    elif [ "$RECENT_ERRORS" -lt 5 ]; then
        echo -e "${YELLOW}⚠${NC} 发现${RECENT_ERRORS}个错误"
        [ "$HEALTH_STATUS" -lt 1 ] && HEALTH_STATUS=1
    else
        echo -e "${RED}✗${NC} 发现${RECENT_ERRORS}个错误，系统可能异常"
        HEALTH_STATUS=2
    fi
    
    # 检查关键告警
    WS_STALE=$(tail -n 100 "$LOG_FILE" 2>/dev/null | grep -c "WebSocket可能断流\|深度数据停止更新" 2>/dev/null || echo "0")
    WS_STALE=$(echo "$WS_STALE" | tr -d '\n\r' | xargs)
    if [ "$WS_STALE" != "0" ] && [ "$WS_STALE" -gt 0 ] 2>/dev/null; then
        echo -e "${RED}✗${NC} WebSocket断流告警 (${WS_STALE}次)"
        HEALTH_STATUS=2
    fi
    
    PANIC_WARN=$(tail -n 100 "$LOG_FILE" 2>/dev/null | grep -c "panic" 2>/dev/null || echo "0")
    PANIC_WARN=$(echo "$PANIC_WARN" | tr -d '\n\r' | xargs)
    if [ "$PANIC_WARN" != "0" ] && [ "$PANIC_WARN" -gt 0 ] 2>/dev/null; then
        echo -e "${RED}✗${NC} 发现Panic异常 (${PANIC_WARN}次)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${RED}✗${NC} 无法检查错误日志"
    HEALTH_STATUS=2
fi
echo ""

# 检查6: 活跃订单数量（可选，需要API配置）
echo -e "${YELLOW}[检查6] 活跃订单检查${NC}"
ACTIVE_ORDERS=$(tail -n 50 "$LOG_FILE" | grep "同步活跃订单" | tail -1 | grep -oP 'active_orders=\K\d+' || echo "N/A")
if [ "$ACTIVE_ORDERS" != "N/A" ]; then
    if [ "$ACTIVE_ORDERS" -gt 5 ]; then
        echo -e "${GREEN}✓${NC} 活跃订单数量正常 (${ACTIVE_ORDERS}个)"
    elif [ "$ACTIVE_ORDERS" -gt 0 ]; then
        echo -e "${YELLOW}⚠${NC} 活跃订单较少 (${ACTIVE_ORDERS}个)"
        [ "$HEALTH_STATUS" -lt 1 ] && HEALTH_STATUS=1
    else
        echo -e "${RED}✗${NC} 没有活跃订单 (可能假死或被限制)"
        HEALTH_STATUS=2
    fi
else
    echo -e "${YELLOW}⚠${NC} 无法获取活跃订单数量"
fi
echo ""

# 最终健康状态
echo "================================================"
if [ "$HEALTH_STATUS" -eq 0 ]; then
    echo -e "${GREEN}整体状态: 健康 ✓${NC}"
    exit 0
elif [ "$HEALTH_STATUS" -eq 1 ]; then
    echo -e "${YELLOW}整体状态: 警告 ⚠${NC}"
    echo "建议: 持续观察，可能需要人工介入"
    exit 1
else
    echo -e "${RED}整体状态: 严重异常 ✗${NC}"
    echo "建议: 立即检查日志，考虑重启进程"
    echo ""
    echo "紧急处理命令:"
    echo "  查看日志: tail -100 $LOG_FILE"
    echo "  重启进程: ./scripts/stop_live.sh && ./scripts/start_live.sh"
    echo "  紧急平仓: ./bin/emergency cancel-all"
    exit 2
fi

