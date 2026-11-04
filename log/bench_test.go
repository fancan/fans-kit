package log

import (
	"context"
	"testing"
)

// 说明：以下基准以“级别被过滤”为主，测量门面调用链的固定开销与字段变参分配，
// 避免文件/终端 I/O 干扰。命中路径的 I/O 与编码开销由 zap 核心承担，
// 本层额外开销可参考 zap 包内的 BenchmarkConvertFields。

// BenchmarkGlobalInfoDisabled 测量全局 log.Info() 在级别被过滤（Info < Error）时的
// 固定开销：仅 atomic.Pointer 加载 + 接口分发 + Enabled 短路，应接近零分配。
func BenchmarkGlobalInfoDisabled(b *testing.B) {
	resetGlobal()
	lg, err := New(Config{Service: "bench", Level: ErrorLevel, Format: "json", Output: []string{"stderr"}})
	if err != nil {
		b.Fatal(err)
	}
	_ = SetGlobal(lg)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		Info("bench")
	}
}

// BenchmarkGlobalInfoFieldsDisabled 测量带 2 个字段的 log.Info() 在级别被过滤时的
// 开销：主要为变参 []Field 切片分配（128B = 2×64B），且发生在调用方构造参数时，
// 即使日志被丢弃也无法避免。
func BenchmarkGlobalInfoFieldsDisabled(b *testing.B) {
	resetGlobal()
	lg, err := New(Config{Service: "bench", Level: ErrorLevel, Format: "json", Output: []string{"stderr"}})
	if err != nil {
		b.Fatal(err)
	}
	_ = SetGlobal(lg)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		Info("bench", String("k", "v"), Int("n", 1))
	}
}

// BenchmarkGlobalInfofDisabled 测量格式化 log.Infof() 在级别被过滤时的开销：
// 相比无参 Info 额外包含 args ...any 变参装箱。
func BenchmarkGlobalInfofDisabled(b *testing.B) {
	resetGlobal()
	lg, err := New(Config{Service: "bench", Level: ErrorLevel, Format: "json", Output: []string{"stderr"}})
	if err != nil {
		b.Fatal(err)
	}
	_ = SetGlobal(lg)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		Infof("bench %d", 1)
	}
}

// BenchmarkInfoContextDisabled 测量 log.InfoContext() 在级别被过滤且 Context 无链路
// 字段时的开销：Enabled 短路使 ContextFields 不会被调用，应零分配。
func BenchmarkInfoContextDisabled(b *testing.B) {
	resetGlobal()
	lg, err := New(Config{Service: "bench", Level: ErrorLevel, Format: "json", Output: []string{"stderr"}})
	if err != nil {
		b.Fatal(err)
	}
	_ = SetGlobal(lg)

	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		InfoContext(ctx, "bench")
	}
}

// BenchmarkInfoContextWithFields 测量 log.InfoContext() 携带链路字段但级别被过滤时的
// 开销：Enabled 短路使其与无字段路径一致，ContextFields 提取不参与。
func BenchmarkInfoContextWithFields(b *testing.B) {
	resetGlobal()
	lg, err := New(Config{Service: "bench", Level: ErrorLevel, Format: "json", Output: []string{"stderr"}})
	if err != nil {
		b.Fatal(err)
	}
	_ = SetGlobal(lg)

	ctx := context.WithValue(context.Background(), TraceIDKey, "trace-1")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		InfoContext(ctx, "bench")
	}
}
