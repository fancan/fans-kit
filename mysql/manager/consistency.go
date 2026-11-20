package manager

import "context"

// 标记上下文，避免写后立即读出现主从延迟的一致性陷阱，把上下文标记一下让读也走主库
// 1、当前主库写
// 2、当前主库请求写入标记 WithPrimary
// 3、当前请求查

type consistencyKey struct{}

// WithPrimary 写入标记
func WithPrimary(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, consistencyKey{}, true)
}

// forcePrimary 读取标记
func forcePrimary(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(consistencyKey{}).(bool)
	return v
}
