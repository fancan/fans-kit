package lock

import (
	"testing"
	"time"
)

// BenchmarkNewToken 量化每次加锁前的令牌生成成本（16 字节 CSPRNG + hex）。
func BenchmarkNewToken(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := newToken(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJitter 量化重试抖动的计算成本（应可忽略）。
func BenchmarkJitter(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = jitter(100 * time.Millisecond)
	}
}

// BenchmarkRedisInt64 量化 Lua 返回值的类型归一成本。
func BenchmarkRedisInt64(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := redisInt64(int64(1)); err != nil {
			b.Fatal(err)
		}
	}
}
