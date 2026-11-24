package lock

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	kitredis "github.com/fancan/fans-kit/redis"
)

// TestNewValidation 覆盖构造参数校验。
func TestNewValidation(t *testing.T) {
	if _, err := New("", time.Second); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty key: want ErrInvalidKey, got %v", err)
	}
	if _, err := New("k", time.Microsecond); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("short ttl: want ErrInvalidTTL, got %v", err)
	}
	l, err := New("k", time.Second)
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if l.Key() != "k" || l.Token() != "" || l.IsHeld() {
		t.Fatalf("unexpected initial state: key=%q token=%q held=%v", l.Key(), l.Token(), l.IsHeld())
	}
}

// TestPackageFuncsRequireRedisInit 验证包级函数在 redis 未初始化时返回 redis 的
// ErrNotInitialized。
func TestPackageFuncsRequireRedisInit(t *testing.T) {
	_ = kitredis.Close()

	if _, _, err := TryAcquire(context.Background(), "k", time.Second); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("TryAcquire: want redis.ErrNotInitialized, got %v", err)
	}
	if _, err := Acquire(context.Background(), "k", time.Second, time.Millisecond); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("Acquire: want redis.ErrNotInitialized, got %v", err)
	}
	if err := WithLock(context.Background(), "k", time.Second, 0, func(context.Context) error { return nil }); !errors.Is(err, kitredis.ErrNotInitialized) {
		t.Fatalf("WithLock: want redis.ErrNotInitialized, got %v", err)
	}
}

// TestUnlockWithoutHolding 验证未持锁时的释放语义。
func TestUnlockWithoutHolding(t *testing.T) {
	l, err := New("k", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Unlock(context.Background()); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("want ErrNotOwner, got %v", err)
	}
	if err := l.Renew(context.Background(), time.Second); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("renew: want ErrNotOwner, got %v", err)
	}
	if ok, err := l.Verify(context.Background()); err != nil || ok {
		t.Fatalf("verify: want (false,nil), got (%v,%v)", ok, err)
	}
}

// TestJitterRange 验证重试抖动落在 [d, d+d/2) 区间，用于打散惊群。
func TestJitterRange(t *testing.T) {
	const d = 100 * time.Millisecond
	for i := 0; i < 1000; i++ {
		got := jitter(d)
		if got < d || got >= d+d/2 {
			t.Fatalf("jitter(%v) = %v, out of range", d, got)
		}
	}
	if jitter(0) != 0 {
		t.Fatal("jitter(0) should be 0")
	}
	if jitter(time.Nanosecond) != time.Nanosecond {
		t.Fatal("tiny duration should be returned unchanged")
	}
}

// TestRedisInt64 验证不同 Redis 实现返回值的兼容解析。
func TestRedisInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int64(1), 1, true},
		{int(2), 2, true},
		{"3", 3, true},
		{[]byte("4"), 4, true},
		{struct{}{}, 0, false},
	}
	for _, c := range cases {
		got, err := redisInt64(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Fatalf("redisInt64(%T) = (%d,%v), want (%d,nil)", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Fatalf("redisInt64(%T) should fail", c.in)
		}
	}
}

// TestLockIntegration 验证加锁/续期/释放/二次释放的完整流程
func TestLockIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR")
	}
	if err := kitredis.Init(kitredis.Config{Addrs: []string{addr}, Prefix: "lock-test:"}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = kitredis.Close() }()

	ctx := context.Background()

	// 包级便捷 API：持锁执行。
	called := false
	if err := WithLock(ctx, "resource", 5*time.Second, 0, func(context.Context) error {
		// 同 key 在持锁期间应抢不到锁。
		if _, ok, err := TryAcquire(ctx, "resource", 5*time.Second); err != nil {
			return err
		} else if ok {
			t.Error("second acquire should fail while held")
		}
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("callback not executed")
	}

	// 手动生命周期：TryLock -> Renew -> Verify -> Unlock -> 二次 Unlock。
	l, err := New("manual", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := l.TryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("TryLock = (%v,%v)", ok, err)
	}
	if err := l.Renew(ctx, 3*time.Second); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if held, err := l.Verify(ctx); err != nil || !held {
		t.Fatalf("Verify = (%v,%v)", held, err)
	}
	if err := l.Unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := l.Unlock(ctx); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("second Unlock: want ErrNotOwner, got %v", err)
	}
}
