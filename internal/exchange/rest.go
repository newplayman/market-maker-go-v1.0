package gateway

import "errors"

// RESTClient 提供最小化的下单接口定义。
type RESTClient interface {
	Place(symbol, side string, price, qty float64) (string, error)
	Cancel(orderID string) error
}

// Client 实现 Gateway，内部委托给 RESTClient。
type Client struct {
	cli RESTClient
}

func NewClient(cli RESTClient) *Client {
	return &Client{cli: cli}
}

func (c *Client) Place(symbol, side string, price, qty float64) (string, error) {
	if c.cli == nil {
		return "", errors.New("rest client not set")
	}
	return c.cli.Place(symbol, side, price, qty)
}

func (c *Client) Cancel(orderID string) error {
	if c.cli == nil {
		return errors.New("rest client not set")
	}
	return c.cli.Cancel(orderID)
}
