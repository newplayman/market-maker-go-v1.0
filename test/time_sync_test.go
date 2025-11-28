package test

import (
	"fmt"
	"os"
	"testing"
	"time"

	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
)

// TestTimeSync 测试时间同步功能
func TestTimeSync(t *testing.T) {
	// 设置环境变量
	os.Setenv("BINANCE_REST_URL", "https://testnet.binancefuture.com")
	os.Setenv("BINANCE_API_KEY", "your_testnet_key")
	os.Setenv("BINANCE_API_SECRET", "your_testnet_secret")

	// 创建时间同步器
	baseURL := os.Getenv("BINANCE_REST_URL")
	timeSync := gateway.NewTimeSync(baseURL)

	// 同步前的本地时间
	localBefore := time.Now().UnixMilli()

	// 执行同步
	err := timeSync.Sync()
	if err != nil {
		t.Logf("⚠️  时间同步失败（可能是网络问题）: %v", err)
		t.Skip("跳过时间同步测试")
	}

	// 同步后获取服务器时间
	serverTime := timeSync.GetServerTime()
	localAfter := time.Now().UnixMilli()

	// 计算偏移量
	offset := timeSync.GetOffset()

	fmt.Printf("\n=== 时间同步测试结果 ===\n")
	fmt.Printf("本地时间（同步前）: %d\n", localBefore)
	fmt.Printf("服务器时间:       %d\n", serverTime)
	fmt.Printf("本地时间（同步后）: %d\n", localAfter)
	fmt.Printf("时间偏移量:       %d 毫秒 (%.2f 秒)\n", offset, float64(offset)/1000)

	// 验证服务器时间在合理范围内
	if serverTime < localBefore-60000 || serverTime > localAfter+60000 {
		t.Errorf("服务器时间异常：超出±60秒范围")
	}

	// 如果偏移量很大，给出警告
	if offset > 5000 || offset < -5000 {
		t.Logf("⚠️  时间偏移量较大: %.2f 秒", float64(offset)/1000)
	} else {
		t.Logf("✓ 时间偏移量正常: %.2f 秒", float64(offset)/1000)
	}

	// 测试多次获取，确保偏移量稳定
	time.Sleep(100 * time.Millisecond)
	serverTime2 := timeSync.GetServerTime()
	diff := serverTime2 - serverTime

	fmt.Printf("\n间隔100ms后服务器时间差: %d 毫秒\n", diff)
	if diff < 50 || diff > 200 {
		t.Logf("⚠️  时间增长异常: %d 毫秒", diff)
	}
}

// TestSignatureWithTimeSync 测试签名中使用时间同步
func TestSignatureWithTimeSync(t *testing.T) {
	baseURL := "https://testnet.binancefuture.com"
	timeSync := gateway.NewTimeSync(baseURL)

	err := timeSync.Sync()
	if err != nil {
		t.Skip("跳过签名测试：无法同步时间")
	}

	// 模拟签名参数
	params := map[string]string{
		"symbol":   "BTCUSDT",
		"side":     "BUY",
		"type":     "LIMIT",
		"quantity": "0.001",
		"price":    "50000",
	}

	// 不设置timestamp，让SignParams自动添加
	secret := "test_secret"
	query, sig := gateway.SignParams(params, secret)

	fmt.Printf("\n=== 签名测试结果 ===\n")
	fmt.Printf("查询字符串: %s\n", query)
	fmt.Printf("签名: %s\n", sig)

	// 验证timestamp已被添加
	if params["timestamp"] == "" {
		t.Error("timestamp未被自动添加")
	} else {
		fmt.Printf("时间戳: %s\n", params["timestamp"])
		t.Logf("✓ 时间戳已自动添加")
	}
}
