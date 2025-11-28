package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ListenKeyClient 管理用户数据流 listenKey（USDC-M）。
type ListenKeyClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type listenKeyResp struct {
	ListenKey string `json:"listenKey"`
}

// NewListenKey 创建 listenKey。
func (c *ListenKeyClient) NewListenKey() (string, error) {
	if c == nil || c.HTTPClient == nil {
		return "", fmt.Errorf("http client not set")
	}
	endpoint := c.BaseURL + "/fapi/v1/listenKey"
	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(nil))
	req.Header.Set("X-MBX-APIKEY", c.APIKey)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("new listenKey status %d", resp.StatusCode)
	}
	var lr listenKeyResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", err
	}
	if lr.ListenKey == "" {
		return "", fmt.Errorf("empty listenKey")
	}
	return lr.ListenKey, nil
}

// KeepAlive 刷新 listenKey 过期时间。
func (c *ListenKeyClient) KeepAlive(listenKey string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	endpoint := c.BaseURL + "/fapi/v1/listenKey"
	req, _ := http.NewRequest(http.MethodPut, endpoint, bytes.NewBuffer(nil))
	req.Header.Set("X-MBX-APIKEY", c.APIKey)
	q := url.Values{}
	q.Set("listenKey", listenKey)
	req.URL.RawQuery = q.Encode()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusTooManyRequests+29 {
		// Binance 429 / 418
		return fmt.Errorf("keepalive rate limited: %d", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("keepalive status %d", resp.StatusCode)
	}
	return nil
}

// CloseListenKey 关闭 listenKey。
func (c *ListenKeyClient) CloseListenKey(listenKey string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	endpoint := c.BaseURL + "/fapi/v1/listenKey"
	req, _ := http.NewRequest(http.MethodDelete, endpoint, bytes.NewBuffer(nil))
	req.Header.Set("X-MBX-APIKEY", c.APIKey)
	q := url.Values{}
	q.Set("listenKey", listenKey)
	req.URL.RawQuery = q.Encode()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("close status %d", resp.StatusCode)
	}
	return nil
}

// NewListenKeyHTTPClient 默认 10s 超时。
func NewListenKeyHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
