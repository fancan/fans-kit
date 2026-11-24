package redis

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR to run integration tests")
	}
	m := newManager()
	ctx := context.Background()
	if err := m.initDefault(ctx, Config{Addrs: []string{addr}, Password: os.Getenv("REDIS_PASSWORD"), Prefix: "test:"}); err != nil {
		t.Fatal(err)
	}
	defer m.close()
	i, err := m.defaultInstance()
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Set(ctx, "key", "value", time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := i.Get(ctx, "key")
	if err != nil || got != "value" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if i.metrics == nil {
		t.Fatal("expected metrics enabled")
	}
	if i.metrics.snapshot().Commands < 2 {
		t.Fatal("expected ping and commands to be observed")
	}
}
