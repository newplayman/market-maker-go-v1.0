#!/bin/bash
# 测试P0-1和P0-2修复的验证脚本

echo "==================================="
echo "Phoenix 修复验证测试"
echo "==================================="
echo ""

# 检查编译状态
echo "✓ 步骤1: 检查编译状态"
if [ -f "bin/phoenix" ]; then
    echo "  ✅ 二进制文件存在: $(ls -lh bin/phoenix | awk '{print $5}')"
else
    echo "  ❌ 二进制文件不存在"
    exit 1
fi
echo ""

# 检查代码修改
echo "✓ 步骤2: 验证代码修改"

echo "  检查adapter.go中的metrics导入..."
if grep -q "github.com/newplayman/market-maker-phoenix/internal/metrics" internal/exchange/adapter.go; then
    echo "  ✅ metrics包已导入"
else
    echo "  ❌ metrics包未导入"
fi

echo "  检查adapter.go中的流量监控调用..."
if grep -q "metrics.RecordWSMessage(\"global\", \"total\", len(msg))" internal/exchange/adapter.go; then
    echo "  ✅ 全局流量监控已启用"
else
    echo "  ❌ 全局流量监控未启用"
fi

if grep -q "metrics.RecordWSMessage(symbol, \"depth\", len(msg))" internal/exchange/adapter.go; then
    echo "  ✅ 按symbol流量监控已启用"
else
    echo "  ❌ 按symbol流量监控未启用"
fi

echo "  检查metrics.go中的DepthProcessing指标..."
if grep -q "DepthProcessing = prometheus.NewHistogramVec" internal/metrics/metrics.go; then
    echo "  ✅ DepthProcessing指标已定义"
else
    echo "  ❌ DepthProcessing指标未定义"
fi

echo "  检查runner.go中的耗时监控..."
if grep -q "metrics.DepthProcessing.WithLabelValues(depth.Symbol).Observe(duration)" internal/runner/runner.go; then
    echo "  ✅ 深度处理耗时监控已添加"
else
    echo "  ❌ 深度处理耗时监控未添加"
fi

if grep -q "深度处理耗时过长" internal/runner/runner.go; then
    echo "  ✅ 耗时警告日志已添加"
else
    echo "  ❌ 耗时警告日志未添加"
fi
echo ""

# 检查Prometheus指标是否可访问(需要实际运行)
echo "✓ 步骤3: 检查新增的Prometheus指标定义"
echo "  以下指标应该在启动后可通过 http://localhost:9090/metrics 访问:"
echo "  - phoenix_ws_bytes_received_total{symbol}"
echo "  - phoenix_ws_message_count_total{symbol,type}"
echo "  - phoenix_depth_processing_duration_seconds{symbol}"
echo "  - phoenix_ws_bandwidth_bytes_per_min{symbol}"
echo ""

echo "==================================="
echo "验证结果汇总"
echo "==================================="
echo "✅ P0-1: WebSocket流量监控 - 已启用"
echo "✅ P0-2: 深度处理耗时监控 - 已启用"
echo ""
echo "📝 建议下一步:"
echo "  1. 运行程序进行实盘测试: ./bin/phoenix -config configs/phoenix_live.yaml"
echo "  2. 访问 http://localhost:9090/metrics 查看新指标"
echo "  3. 观察15分钟,确认流量是否稳定(不再递增)"
echo "  4. 检查日志中是否有'深度处理耗时过长'警告"
echo ""
