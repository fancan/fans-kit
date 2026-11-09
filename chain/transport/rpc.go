package transport

// chain的传输层 与以太坊生态的底层传输交互
// 只做与链交互的封装

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/rpc"
)

// Client 是 go-ethereum rpc.Client的轻量包装，统一 HTTP/WS 的调用入口，
// 并通过原子布尔标记关闭状态（幂等 Close）。
type Client struct {
	rpc    *rpc.Client
	closed atomic.Bool
}

// Dial 按端点地址拨号（HTTP或WS），并附带自定义请求头。
func Dial(ctx context.Context, endpoint string, headers http.Header) (*Client, error) {
	opts := []rpc.ClientOption{}
	if headers != nil {
		opts = append(opts, rpc.WithHeaders(headers))
	}
	c, err := rpc.DialOptions(ctx, endpoint, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{rpc: c}, nil
}

// Call 发起一次RPC调用。
func (c *Client) Call(ctx context.Context, result any, method string, args ...any) error {
	return c.rpc.CallContext(ctx, result, method, args...)
}

// Batch 发起一次JSON-RPC批处理。
func (c *Client) Batch(ctx context.Context, elems []rpc.BatchElem) error {
	return c.rpc.BatchCallContext(ctx, elems)
}

// Subscribe 建立订阅。namespace 为真实命名空间（如 eth/shh），
// 对应 `<namespace>_subscribe` 方法；订阅子类型（如 logs/newHeads）应作为 args 传入。
func (c *Client) Subscribe(ctx context.Context, namespace string, channel any, args ...any) (*rpc.ClientSubscription, error) {
	return c.rpc.Subscribe(ctx, namespace, channel, args...)
}

// Raw 返回底层的 go-ethereum rpc.Client（高级用途）。
func (c *Client) Raw() *rpc.Client { return c.rpc }

// Close 幂等关闭客户端。
func (c *Client) Close() {
	if c != nil && c.rpc != nil && c.closed.CompareAndSwap(false, true) {
		c.rpc.Close()
	}
}

// Closed 返回客户端是否已关闭。
func (c *Client) Closed() bool { return c == nil || c.closed.Load() }
