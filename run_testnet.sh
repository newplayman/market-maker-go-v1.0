#!/bin/bash
# Phoenix v2 测试网快速启动脚本

set -e

echo "=========================================="
echo "Phoenix v2 测试网启动脚本"
echo "=========================================="
echo ""

# 检查配置文件
if [ ! -f "config.testnet.yaml" ]; then
    echo "❌ 错误: 找不到 config.testnet.yaml"
    echo "请先创建配置文件"
    exit 1
fi

# 检查API密钥是否已配置
if grep -q "YOUR_TESTNET_API_KEY" config.testnet.yaml; then
    echo "⚠️  警告: 检测到默认API密钥"
    echo ""
    echo "请先编辑 config.testnet.yaml 文件，填入你的测试网API密钥："
    echo "  api_key: \"你的API_KEY\""
    echo "  api_secret: \"你的API_SECRET\""
    echo ""
    read -p "是否已经配置好API密钥？(y/n) " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "请先配置API密钥后再运行"
        exit 1
    fi
fi

# 创建数据目录
echo "📁 创建数据目录..."
mkdir -p data

# 检查是否需要编译
if [ ! -f "bin/phoenix" ]; then
    echo "🔨 首次运行，正在编译..."
    make build
    echo "✅ 编译完成"
else
    echo "✅ 可执行文件已存在"
fi

echo ""
echo "=========================================="
echo "🚀 启动Phoenix测试网做市系统"
echo "=========================================="
echo ""
echo "配置信息:"
echo "  - 配置文件: config.testnet.yaml"
echo "  - 日志级别: info"
echo "  - 监控端口: http://localhost:9090/metrics"
echo "  - 测试网: Binance Futures Testnet"
echo ""
echo "提示:"
echo "  - 按 Ctrl+C 停止系统"
echo "  - 查看日志了解运行状态"
echo "  - 访问 https://testnet.binancefuture.com 查看账户"
echo ""
echo "=========================================="
echo ""

# 启动系统
./bin/phoenix -config config.testnet.yaml -log info
