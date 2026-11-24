package cache

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitredis "github.com/fancan/fans-kit/redis"
)

// TestPackageFuncsRequireRedisInit 验证包级函数在 redis 未初始化时返回 redis 的
// ErrNotInitialized，而不是 nil 解引用或静默失败。
func TestPackageFuncsRequireRedisInit(t *testing.T) {
	_ = kitredis.Close() // 确保未初始化

	var v struct{}
	if err := Get(context.Background(), "k", &v); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("Get: want redis.ErrNotInitialized, got %v", err)
	}
	if err := Set(context.Background(), "k", v, time.Minute); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("Set: want redis.ErrNotInitialized, got %v", err)
	}
	if err := Delete(context.Background(), "k"); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("Delete: want redis.ErrNotInitialized, got %v", err)
	}
	if err := Remember(context.Background(), "k", time.Minute, &v, func(context.Context) (any, error) {
		return v, nil
	}); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("Remember: want redis.ErrNotInitialized, got %v", err)
	}
}

// TestRememberNilLoader 验证 nil loader 在触达 redis 之前就被拒绝。
func TestRememberNilLoader(t *testing.T) {
	_ = kitredis.Close()
	var v struct{}
	if err := Remember(context.Background(), "k", time.Minute, &v, nil); !errors.Is(err, ErrNilLoader) {
		t.Fatalf("want ErrNilLoader, got %v", err)
	}
}

// TestRememberSingleflightIntegration 验证同一 key 的并发加载被合并为一次，
// 且所有等待者都拿到同一份数据。
//
// 同时覆盖回归点：singleflight 状态必须是包级共享，否则合并会完全失效。
func TestRememberSingleflightIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR")
	}
	if err := kitredis.Init(kitredis.Config{Addrs: []string{addr}, Prefix: "cache-test:"}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = kitredis.Close() }()

	var calls atomic.Int32
	type payload struct{ Value string }
	loader := func(context.Context) (any, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return payload{Value: "ok"}, nil
	}

	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var p payload
			if err := Remember(context.Background(), "one", time.Minute, &p, loader); err != nil {
				t.Error(err)
			} else if p.Value != "ok" {
				t.Errorf("bad value: %+v", p)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times, want 1", calls.Load())
	}
}
