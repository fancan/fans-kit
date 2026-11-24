package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func benchmarkInstance(b *testing.B) *Instance {
	b.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		b.Skip("set REDIS_ADDR to run Redis benchmarks")
	}
	cfg := Config{Mode: ModeStandalone, Addrs: []string{addr}, Password: os.Getenv("REDIS_PASSWORD"), PoolSize: 64, MinIdleConns: 8, ReadTimeout: time.Second, WriteTimeout: time.Second, Prefix: "bench:"}
	if err := InitContext(context.Background(), cfg); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = Close() })
	i, err := Default()
	if err != nil {
		b.Fatal(err)
	}
	return i
}

func BenchmarkRedisSet(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; b.Loop(); n++ {
		if err := i.Set(ctx, fmt.Sprintf("set:%d", n), "value", time.Minute); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRedisGet(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	_ = i.Set(ctx, "get:key", "value", time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; b.Loop(); n++ {
		if _, err := i.Get(ctx, "get:key"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRedisPipeline10(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	c := i.Client()
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; b.Loop(); n++ {
		p := c.Pipeline()
		for j := 0; j < 10; j++ {
			p.Set(ctx, i.Key(fmt.Sprintf("pipe:%d:%d", n, j)), "v", time.Minute)
		}
		if _, err := p.Exec(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRedisConcurrentGet(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	_ = i.Set(ctx, "concurrent:key", "value", time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := i.Get(ctx, "concurrent:key"); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkRedisMGet10(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	keys := make([]string, 10)
	for n := range keys {
		keys[n] = fmt.Sprintf("mget:%d", n)
		_ = i.Set(ctx, keys[n], "value", time.Hour)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; b.Loop(); n++ {
		if _, err := i.MGet(ctx, keys...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisJSON(b *testing.B) {
	i := benchmarkInstance(b)
	ctx := context.Background()
	value := struct {
		Symbol   string `json:"symbol"`
		Decimals int    `json:"decimals"`
	}{Symbol: "WETH", Decimals: 18}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; b.Loop(); n++ {
		if err := i.SetJSON(ctx, "json:value", value, time.Hour); err != nil {
			b.Fatal(err)
		}
		var dst struct {
			Symbol   string `json:"symbol"`
			Decimals int    `json:"decimals"`
		}
		if err := i.GetJSON(ctx, "json:value", &dst); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- 以下基准不依赖真实 Redis，用于量化「每次命令的固定开销」。 ----

// BenchmarkMetricsObserve 量化 metrics hook 对每条命令附加的观测开销
// （含命令名归一化、sync.Map 查找、3 次原子加、直方图分桶）。
func BenchmarkMetricsObserve(b *testing.B) {
	m := newMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.observe("get", 500*time.Microsecond, nil)
	}
}

// BenchmarkMetricsObserveUnknownCommand 量化未知命令（归入 "other"）的往返开销，
// 用于确认标签收敛不会引入额外分支成本。
func BenchmarkMetricsObserveUnknownCommand(b *testing.B) {
	m := newMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.observe("XTRACUSTOM", 500*time.Microsecond, nil)
	}
}

// BenchmarkInstanceKey 量化 key 前缀拼接的分配成本（每条命令一次）。
func BenchmarkInstanceKey(b *testing.B) {
	i := &Instance{prefix: "dex:pool:"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = i.Key("user:1001:balance")
	}
}

// BenchmarkMetricsSnapshot 量化 Prometheus 抓取时构建快照的成本（非热路径）。
func BenchmarkMetricsSnapshot(b *testing.B) {
	m := newMetrics()
	for _, cmd := range []string{"get", "set", "hget", "hset", "lpush", "zadd", "eval", "other"} {
		m.observe(cmd, time.Millisecond, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.snapshot()
	}
}
