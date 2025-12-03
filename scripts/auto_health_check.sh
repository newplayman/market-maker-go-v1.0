#!/bin/bash

# Phoenix做市商自动健康检查守护脚本
# 用途：定期执行健康检查，发现异常时自动告警并可选自动恢复

# 配置
HEALTH_CHECK_SCRIPT="./scripts/health_check.sh"
CHECK_INTERVAL=30  # 检查间隔（秒）
LOG_FILE="logs/health_check.log"
MAX_FAILURES=3     # 连续失败次数阈值
AUTO_RESTART=false # 是否自动重启（谨慎开启）

# 状态记录
CONSECUTIVE_FAILURES=0

echo "启动Phoenix健康检查守护进程..."
echo "检查间隔: ${CHECK_INTERVAL}秒"
echo "日志文件: $LOG_FILE"
echo "自动重启: $AUTO_RESTART"
echo ""

mkdir -p logs

while true; do
    TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')
    
    # 执行健康检查
    if $HEALTH_CHECK_SCRIPT >> "$LOG_FILE" 2>&1; then
        # 健康检查通过
        CONSECUTIVE_FAILURES=0
        echo "[$TIMESTAMP] ✓ 健康检查通过" | tee -a "$LOG_FILE"
    else
        EXIT_CODE=$?
        CONSECUTIVE_FAILURES=$((CONSECUTIVE_FAILURES + 1))
        
        if [ $EXIT_CODE -eq 1 ]; then
            # 警告状态
            echo "[$TIMESTAMP] ⚠ 健康检查警告 (连续失败: $CONSECUTIVE_FAILURES)" | tee -a "$LOG_FILE"
        else
            # 严重异常
            echo "[$TIMESTAMP] ✗ 健康检查失败 (连续失败: $CONSECUTIVE_FAILURES)" | tee -a "$LOG_FILE"
            
            # 如果连续失败超过阈值
            if [ $CONSECUTIVE_FAILURES -ge $MAX_FAILURES ]; then
                echo "[$TIMESTAMP] 【严重】连续失败${CONSECUTIVE_FAILURES}次，触发告警！" | tee -a "$LOG_FILE"
                
                # 发送告警（可以扩展为企业微信、邮件等）
                echo "[$TIMESTAMP] TODO: 发送告警通知" | tee -a "$LOG_FILE"
                
                # 自动重启（谨慎）
                if [ "$AUTO_RESTART" = true ]; then
                    echo "[$TIMESTAMP] 尝试自动重启进程..." | tee -a "$LOG_FILE"
                    ./scripts/stop_live.sh >> "$LOG_FILE" 2>&1
                    sleep 5
                    ./scripts/start_live.sh >> "$LOG_FILE" 2>&1
                    CONSECUTIVE_FAILURES=0
                    sleep 30  # 等待启动完成
                fi
            fi
        fi
    fi
    
    # 等待下一次检查
    sleep $CHECK_INTERVAL
done


