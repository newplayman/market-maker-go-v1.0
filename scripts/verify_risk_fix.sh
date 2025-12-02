#!/bin/bash

# Phoenix风控修复验证脚本
# 用于验证所有风控修复是否正确实施

echo "================================================"
echo "Phoenix 风控修复验证脚本"
echo "================================================"
echo ""

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 计数器
pass_count=0
fail_count=0

# 检查函数
check() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
        ((pass_count++))
    else
        echo -e "${RED}✗${NC} $2"
        ((fail_count++))
    fi
}

echo "1. 检查配置文件修改"
echo "-----------------------------------"

# 检查 net_max
net_max=$(grep "net_max:" configs/phoenix_live.yaml | grep -v "^[[:space:]]*#" | head -1 | awk '{print $2}')
if [ "$net_max" == "0.50" ]; then
    check 0 "net_max已降低到0.50"
else
    check 1 "net_max未修改（当前: $net_max，应为: 0.50）"
fi

# 检查 grinding_thresh
grinding_thresh=$(grep "grinding_thresh:" configs/phoenix_live.yaml | grep -v "^[[:space:]]*#" | head -1 | awk '{print $2}')
if [ "$grinding_thresh" == "0.30" ]; then
    check 0 "grinding_thresh已降低到0.30"
else
    check 1 "grinding_thresh未修改（当前: $grinding_thresh，应为: 0.30）"
fi

echo ""
echo "2. 检查代码修改"
echo "-----------------------------------"

# 检查 risk.go 中的持仓绝对值检查
if grep -q "先检查当前持仓是否已经超标" internal/risk/risk.go; then
    check 0 "risk.go已增加持仓绝对值检查"
else
    check 1 "risk.go未增加持仓绝对值检查"
fi

# 检查 grinding.go 中的波动率放宽
if grep -q "0.01.*1%.*波动率" internal/strategy/grinding.go; then
    check 0 "grinding.go已放宽波动率限制到1%"
else
    check 1 "grinding.go波动率限制未修改"
fi

# 检查 strategy.go 中的熔断机制
if grep -q "紧急熔断" internal/strategy/strategy.go; then
    check 0 "strategy.go已增加紧急熔断机制"
else
    check 1 "strategy.go未增加紧急熔断机制"
fi

# 检查 import 语句
if grep -q '"github.com/rs/zerolog/log"' internal/strategy/grinding.go; then
    check 0 "grinding.go已导入log包"
else
    check 1 "grinding.go未导入log包"
fi

if grep -q '"fmt"' internal/strategy/strategy.go; then
    check 0 "strategy.go已导入fmt包"
else
    check 1 "strategy.go未导入fmt包"
fi

echo ""
echo "3. 检查编译状态"
echo "-----------------------------------"

# 检查二进制文件是否存在且是最新的
if [ -f "bin/phoenix" ]; then
    check 0 "Phoenix二进制文件存在"
    
    # 检查是否是最近编译的（5分钟内）
    file_age=$(($(date +%s) - $(stat -c %Y bin/phoenix)))
    if [ $file_age -lt 300 ]; then
        check 0 "二进制文件是最新编译的（${file_age}秒前）"
    else
        check 1 "二进制文件较旧（${file_age}秒前），建议重新编译"
    fi
else
    check 1 "Phoenix二进制文件不存在，需要编译"
fi

echo ""
echo "4. 检查文档"
echo "-----------------------------------"

if [ -f "docs/RISK_CONTROL_FIX_2025-12-02.md" ]; then
    check 0 "修复报告已创建"
else
    check 1 "修复报告不存在"
fi

if [ -f "风控修复总结.md" ]; then
    check 0 "修复总结已创建"
else
    check 1 "修复总结不存在"
fi

echo ""
echo "================================================"
echo "验证结果汇总"
echo "================================================"
echo -e "通过: ${GREEN}${pass_count}${NC}"
echo -e "失败: ${RED}${fail_count}${NC}"
echo ""

if [ $fail_count -eq 0 ]; then
    echo -e "${GREEN}✓ 所有检查通过！可以部署。${NC}"
    echo ""
    echo "建议部署步骤："
    echo "1. 停止当前实例: ./scripts/stop_live.sh"
    echo "2. 备份配置和数据"
    echo "3. 启动新版本: ./scripts/start_live.sh"
    echo "4. 监控日志: tail -f logs/phoenix_live.out | grep -E '风控|熔断|Grinding'"
    exit 0
else
    echo -e "${RED}✗ 有检查失败，请修复后再部署。${NC}"
    exit 1
fi

