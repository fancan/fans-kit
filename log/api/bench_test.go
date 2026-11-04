package api

import (
	"context"
	"testing"
)

// BenchmarkContextFieldsEmpty 测量空 Context 的字段提取成本，用于验证零分配优化：
// 类型化整型key消除了字符串key每次ctx.Value的装箱分配。
func BenchmarkContextFieldsEmpty(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = ContextFields(ctx)
	}
}

// BenchmarkContextFieldsWithValues 测量命中2个链路字段时的提取成本：
// 包含fields切片分配（make([]Field, 0, 5)）与ctx.Value取值。
func BenchmarkContextFieldsWithValues(b *testing.B) {
	ctx := context.WithValue(context.Background(), TraceIDKey, "trace-1")
	ctx = context.WithValue(ctx, ReqIDKey, "req-1")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = ContextFields(ctx)
	}
}
