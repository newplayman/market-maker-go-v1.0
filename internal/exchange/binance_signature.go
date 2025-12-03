package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 可覆盖的时间函数，便于测试。
var timeNowMillis = func() int64 { return time.Now().UnixMilli() }

// 全局时间同步器（可被外部设置）
var globalTimeSync *TimeSync

// SetGlobalTimeSync 允许外部设置全局时间同步器
func SetGlobalTimeSync(ts *TimeSync) {
	globalTimeSync = ts
}

// SignParams 生成 Binance 所需的签名（API key/secret 外部提供）。
func SignParams(params map[string]string, secret string) (string, string) {
	// 添加 timestamp（若未提供）
	if _, ok := params["timestamp"]; !ok {
		if globalTimeSync != nil {
			params["timestamp"] = fmt.Sprint(globalTimeSync.GetServerTime())
		} else {
			params["timestamp"] = fmt.Sprint(timeNowMillis())
		}
	}
	// 排序拼接 query
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(params[k]))
	}
	query := b.String()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(query))
	return query, hex.EncodeToString(mac.Sum(nil))
}
