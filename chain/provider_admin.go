package chain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fancan/fans-kit/chain/config"
	"github.com/fancan/fans-kit/chain/provider"
)

// AddProvider 运行期向链动态新增提供商（HTTP/WS 分别拨号并入池）。
// 若 WS 部分失败会回滚已加入的 HTTP 端点，保证不会残留半加入状态。
func (i *Instance) AddProvider(ctx context.Context, p config.ProviderConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()
	ctx = dialCtx
	p.HTTPURL = os.ExpandEnv(p.HTTPURL)
	p.WSURL = os.ExpandEnv(p.WSURL)
	p.APIKey = os.ExpandEnv(p.APIKey)
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if p.HTTPURL == "" && p.WSURL == "" {
		return fmt.Errorf("provider %s: http_url or ws_url required", p.Name)
	}
	if p.Weight <= 0 {
		p.Weight = 1
	}
	headers := make(map[string]string, len(p.Headers))
	for k, v := range p.Headers {
		headers[k] = v
	}
	p.Headers = headers
	for k, v := range p.Headers {
		p.Headers[k] = os.ExpandEnv(v)
	}
	if p.APIKey != "" && p.APIKeyHeader != "" {
		p.Headers[p.APIKeyHeader] = p.APIKey
	}
	if err := config.ValidateProvider(p); err != nil {
		return err
	}
	if p.HTTPURL != "" {
		hc := p
		hc.WSURL = ""
		e, err := provider.NewEndpoint(ctx, hc)
		if err != nil {
			return err
		}
		if err = i.httpPool.Add(e); err != nil {
			e.Close()
			return err
		}
	}
	if p.WSURL != "" {
		wc := p
		wc.HTTPURL = ""
		e, err := provider.NewEndpoint(ctx, wc)
		if err != nil {
			_ = i.httpPool.Remove(p.Name)
			return err
		}
		if err = i.wsPool.Add(e); err != nil {
			e.Close()
			_ = i.httpPool.Remove(p.Name)
			return err
		}
	}
	return nil
}

// RemoveProvider 运行期移除指定提供商（HTTP/WS 池一并移除）并关闭其连接。
func (i *Instance) RemoveProvider(id string) error {
	var errs []error
	if i.httpPool.Get(id) != nil {
		if e := i.httpPool.Remove(id); e != nil {
			errs = append(errs, e)
		}
	}
	if i.wsPool.Get(id) != nil {
		if e := i.wsPool.Remove(id); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

// Providers 返回该链全部提供商（HTTP + WS）的健康快照。
func (i *Instance) Providers() []provider.Snapshot {
	out := i.httpPool.Snapshots()
	out = append(out, i.wsPool.Snapshots()...)
	return out
}

// SetHTTPSelector 为 HTTP RPC 调用安装自定义提供商选择策略。
func (i *Instance) SetHTTPSelector(s provider.Selector) { i.httpPool.SetSelector(s) }

// SetWSSelector 为 WebSocket 连接安装自定义提供商选择策略。
func (i *Instance) SetWSSelector(s provider.Selector) { i.wsPool.SetSelector(s) }
