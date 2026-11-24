// Package lock 是基于 Redis 的令牌租约分布式锁。
//
// 实现要点：
//  1. 加锁使用 SET NX PX，锁值是一个随机令牌；
//  2. 释放 / 续期使用 Lua 脚本，先比对令牌再操作，避免误删他人的锁。
package lock

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	kitredis "github.com/fancan/fans-kit/redis"
)

var (
	ErrNotOwner    = errors.New("lock: lock is no longer owned by this token")
	ErrAlreadyHeld = errors.New("lock: this Locker already holds the lock")
	ErrInvalidKey  = errors.New("lock: key cannot be empty")
	ErrInvalidTTL  = errors.New("lock: ttl must be at least one millisecond")
)

// 释放锁：令牌匹配才删除。
const releaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end`

// 续期：令牌匹配才重置 TTL。
const renewScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) else return 0 end`

// defaultRetry 是 Lock/Acquire 的默认重试间隔。
const defaultRetry = 100 * time.Millisecond

// Locker 是一把分布式锁的句柄。
//
// 注意：一个 Locker 实例只代表一次「加锁会话」。不要在相互独立的临界区之间
// 复用同一个 Locker（第二次加锁会返回 ErrAlreadyHeld）；需要多次加锁请每次 New。
type Locker struct {
	mu    sync.Mutex    // 保护 token/held/ttl，并串行化锁操作
	key   string        // 锁 key
	token string        // 本次持有的随机令牌
	ttl   time.Duration // 锁过期时间
	held  bool          // 是否已持锁（本进程视角）
}

// New 创建一把锁（不立即加锁）
func New(key string, ttl time.Duration) (*Locker, error) {
	if key == "" {
		return nil, ErrInvalidKey
	}
	if ttl < time.Millisecond {
		return nil, ErrInvalidTTL
	}
	return &Locker{key: key, ttl: ttl}, nil
}

// getRedisInstance 返回每次锁操作使用的 redis 实例 注意：这里返回的是默认的，实际多业务线这里需要稍微改造一下匹配到自己业务线的
func getRedisInstance() (*kitredis.Instance, error) {
	return kitredis.Default()
}

// newToken 生成 128 位随机令牌。
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		return "", fmt.Errorf("lock: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Key 返回锁 key。
func (l *Locker) Key() string { return l.key }

// Token 返回当前持有的令牌；未持锁时为空字符串。
func (l *Locker) Token() string { l.mu.Lock(); defer l.mu.Unlock(); return l.token }

// IsHeld 返回本进程视角下是否持锁。
//
// 注意：这是本地状态，不代表锁在 Redis 中一定仍然有效：若进程暂停或网络分区导致
// TTL 到期，锁可能已被他人获取。对一致性敏感的临界区应在每次操作前用
// Verify 或 Renew 确认所有权。
func (l *Locker) IsHeld() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.held }

// TryLock 尝试加锁一次，不等待。
func (l *Locker) TryLock(ctx context.Context) (bool, error) {
	ctx = nonNilContext(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.held {
		return false, ErrAlreadyHeld
	}
	inst, err := getRedisInstance()
	if err != nil {
		return false, err
	}
	token, err := newToken()
	if err != nil {
		return false, err
	}
	ok, err := inst.SetNX(ctx, l.key, token, l.ttl)
	if err != nil {
		return false, err
	}
	if ok {
		l.token = token
		l.held = true
	}
	return ok, nil
}

// Lock 阻塞式加锁，直到成功、ctx 取消或出错。
// retry 是重试间隔，<=0 时取 100ms。实际间隔会加入 [0, retry/2) 的随机抖动，避免大量等待者在同一时刻同时重试（惊群）。
func (l *Locker) Lock(ctx context.Context, retry time.Duration) error {
	ctx = nonNilContext(ctx)
	if l.IsHeld() {
		return ErrAlreadyHeld
	}
	if retry <= 0 {
		retry = defaultRetry
	}

	timer := time.NewTimer(jitter(retry))
	defer timer.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := l.TryLock(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(jitter(retry))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// jitter 返回 [d, d+d/2) 区间内的随机时长，用于打散重试。
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	half := int64(d / 2)
	if half <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(half))
}

// Unlock 释放锁。只有令牌仍然匹配才会真正删除。
// 注意：若锁已过期（或被他人获取），返回 ErrNotOwner，并清除本地持有状态。
func (l *Locker) Unlock(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return ErrNotOwner
	}
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}
	v, err := inst.Eval(ctx, releaseScript, []string{l.key}, l.token)
	if err != nil {
		// 网络/超时等错误：所有权未知，保留 held 以便调用方重试释放。
		return err
	}
	n, err := redisInt64(v)
	if err != nil {
		return err
	}
	l.held = false
	l.token = ""
	if n == 0 {
		return ErrNotOwner
	}
	return nil
}

// Renew 续期。只有令牌仍然匹配才会重置 TTL。
// 注意：返回 ErrNotOwner 表示锁已经丢失（过期或被他人持有），此时应立即停止临界区操作。
func (l *Locker) Renew(ctx context.Context, ttl time.Duration) error {
	ctx = nonNilContext(ctx)
	if ttl < time.Millisecond {
		return ErrInvalidTTL
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return ErrNotOwner
	}
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}
	v, err := inst.Eval(ctx, renewScript, []string{l.key}, l.token, ttl.Milliseconds())
	if err != nil {
		return err
	}
	n, err := redisInt64(v)
	if err != nil {
		return err
	}
	if n == 0 {
		l.held = false
		l.token = ""
		return ErrNotOwner
	}
	l.ttl = ttl
	return nil
}

// Verify 检查锁是否仍由本 Locker 持有（只读，不改变状态）。
//
// 用于长临界区内周期性自检：一旦发现锁已丢失，应用应中止后续写操作。
func (l *Locker) Verify(ctx context.Context) (bool, error) {
	ctx = nonNilContext(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return false, nil
	}
	inst, err := getRedisInstance()
	if err != nil {
		return false, err
	}
	cur, err := inst.Get(ctx, l.key)
	if err != nil {
		if errors.Is(err, kitredis.Nil) {
			l.held = false
			l.token = ""
			return false, nil
		}
		return false, err
	}
	if cur != l.token {
		l.held = false
		l.token = ""
		return false, nil
	}
	return true, nil
}

// nonNilContext 兜底避免把 nil context 传给 go-redis。
func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// redisInt64 兼容不同 Redis 实现/代理对 Lua 返回值的编码差异。
func redisInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case string:
		var out int64
		if _, err := fmt.Sscan(n, &out); err != nil {
			return 0, fmt.Errorf("lock: unexpected Redis script result %q", n)
		}
		return out, nil
	case []byte:
		var out int64
		if _, err := fmt.Sscan(string(n), &out); err != nil {
			return 0, fmt.Errorf("lock: unexpected Redis script result %q", n)
		}
		return out, nil
	default:
		return 0, fmt.Errorf("lock: unexpected Redis script result %T", v)
	}
}

// ---- 包级零侵入函数 ----

// TryAcquire 尝试获取 key 上的锁，不等待。
// 成功时返回的 *Locker 由调用方负责 Unlock；返回 (nil, false, nil) 表示锁被他人持有。
func TryAcquire(ctx context.Context, key string, ttl time.Duration) (*Locker, bool, error) {
	l, err := New(key, ttl)
	if err != nil {
		return nil, false, err
	}
	ok, err := l.TryLock(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return l, true, nil
}

// Acquire 阻塞式获取 key 上的锁。retry <= 0 时使用默认间隔。
func Acquire(ctx context.Context, key string, ttl, retry time.Duration) (*Locker, error) {
	l, err := New(key, ttl)
	if err != nil {
		return nil, err
	}
	if err := l.Lock(ctx, retry); err != nil {
		return nil, err
	}
	return l, nil
}

// WithLock 在持有锁的前提下执行 fn，并在返回时自动释放。
//
//   - 加锁失败/ctx 取消：不执行 fn，直接返回错误；
//   - fn 返回错误：原样返回，同时仍会尝试释放锁；
//   - fn 发生 panic：释放锁后继续向上抛出。
func WithLock(ctx context.Context, key string, ttl, retry time.Duration, fn func(ctx context.Context) error) error {
	if fn == nil {
		return errors.New("lock: nil callback")
	}
	l, err := Acquire(ctx, key, ttl, retry)
	if err != nil {
		return err
	}
	defer func() {
		// 释放失败不应掩盖 fn 的错误；锁最终会随 TTL 过期。
		_ = l.Unlock(context.WithoutCancel(nonNilContext(ctx)))
	}()
	return fn(ctx)
}

// WithTryLock 尝试持锁执行 fn，未抢到锁时返回 (false, nil) 且不执行 fn。
func WithTryLock(ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) error) (bool, error) {
	if fn == nil {
		return false, errors.New("lock: nil callback")
	}
	l, ok, err := TryAcquire(ctx, key, ttl)
	if err != nil || !ok {
		return false, err
	}
	defer func() {
		_ = l.Unlock(context.WithoutCancel(nonNilContext(ctx)))
	}()
	return true, fn(ctx)
}
