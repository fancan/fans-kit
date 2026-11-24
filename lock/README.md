# Lock —— 基于 Redis 的令牌租约分布式锁

`lock` 是构建在 `fans-kit/redis` 之上的分布式锁，采用 **SET NX PX + 随机令牌 + Lua 校验**
实现，保证「只有锁的真正持有者才能释放/续期」，避免误删他人持有的锁。

## 特性

| 能力 | 说明 |
| --- | --- |
| 安全释放 | Lua 先比对令牌再 DEL，锁过期后被他人获取也不会误删 |
| 安全续期 | Lua 先比对令牌再 PEXPIRE，续期失败明确告知「锁已丢失」 |
| 抢锁不惊群 | 阻塞式重试带随机抖动，打散大量等待者 |
| 所有权自检 | `Verify` 周期性确认锁仍在自己手里 |
| 三种用法 | 一次性 `TryLock` / 阻塞 `Lock` / 回调式 `WithLock`（自动释放） |
| 连接由 redis 管理 | 底层实例每次操作时经 `redis.Default()` 惰性解析，`redis.Close` 统一释放连接 |

## 生命周期：只初始化 redis，lock 开箱即用

```go
import (
    kitredis "github.com/fancan/fans-kit/redis"
    "github.com/fancan/fans-kit/lock"
)

func main() {
    if err := kitredis.Init(kitredis.Config{
        Addrs:  []string{"127.0.0.1:6379"},
        Prefix: "app:",
    }); err != nil {
        log.Fatal(err)
    }
    defer kitredis.Close()

    // 直接使用，无需 lock.Init / lock.Close
    err := lock.WithLock(context.Background(), "job:daily-report",
        30*time.Second, 100*time.Millisecond, func(ctx context.Context) error {
            return doReport(ctx)
        })
    _ = err
}
```

- `lock` **没有自己的 `Init`/`Close`**：连接的开/关完全由 `redis` 模块负责。
- 未调用 `redis.Init` 时，包级函数会返回 `redis.ErrNotInitialized`（用
  `errors.Is(err, kitredis.ErrNotInitialized)` 判断）。

## 三种用法

### 1. 回调式 `WithLock`（推荐，最不容易漏解锁）

```go
err := lock.WithLock(ctx, "job:daily-report", 30*time.Second, 100*time.Millisecond,
    func(ctx context.Context) error {
        // 临界区：只有持锁者进入
        return doReport(ctx)
    })
```

自动加锁、自动释放（即使回调 panic 也会释放）。

### 2. 回调式 `WithTryLock`（非阻塞，抢不到就跳过）

```go
ran, err := lock.WithTryLock(ctx, "job:snapshot", 30*time.Second,
    func(ctx context.Context) error {
        return snapshot(ctx)
    })
if !ran {
    // 别的实例正在跑，本次跳过（例如定时任务去重）
}
```

### 3. 手动持锁（需要长时间/分阶段临界区）

```go
l, err := lock.Acquire(ctx, "resource:42", 10*time.Second, 100*time.Millisecond)
if err != nil {
    return err
}
defer l.Unlock(context.WithoutCancel(ctx))

// 长任务中定期续期并确认所有权
if err := l.Renew(ctx, 10*time.Second); err != nil {
    if errors.Is(err, lock.ErrNotOwner) {
        // 锁已丢失，立即停止写操作
        return err
    }
}
```

或先构造句柄、稍后再加锁：

```go
l, err := lock.New("resource:42", 10*time.Second) // 只校验参数，不立即加锁

ok, err := l.TryLock(ctx)                       // 非阻塞抢一次
// 或 err = l.Lock(ctx, 100*time.Millisecond)  // 阻塞抢
```

## API 一览

### 包级函数

| 函数 | 说明 |
| --- | --- |
| `TryAcquire(ctx, key, ttl)` | 非阻塞抢锁，返回 `(*Locker, bool, error)`；`(nil,false,nil)` 表示被他人持有 |
| `Acquire(ctx, key, ttl, retry)` | 阻塞抢锁，返回持锁的 `*Locker` |
| `WithLock(ctx, key, ttl, retry, fn)` | 持锁执行 fn 并自动释放 |
| `WithTryLock(ctx, key, ttl, fn)` | 抢到才执行，返回 `(是否执行, error)` |

### `*Locker`

| 方法 | 说明 |
| --- | --- |
| `New(key, ttl)` | 构造（不立即加锁、不连接 Redis）；校验 key 非空、ttl ≥ 1ms |
| `TryLock(ctx)` | 尝试加锁一次；已持锁返回 `ErrAlreadyHeld` |
| `Lock(ctx, retry)` | 阻塞加锁，`retry <= 0` 取默认 100ms，实际间隔带随机抖动 |
| `Unlock(ctx)` | 令牌匹配才删除；锁已丢失返回 `ErrNotOwner` |
| `Renew(ctx, ttl)` | 令牌匹配才续期；锁已丢失返回 `ErrNotOwner` |
| `Verify(ctx)` | 只读校验锁是否仍在自己手里（`bool, error`） |
| `Token()` / `Key()` / `IsHeld()` | 查询本地状态 |

## 语义与错误

| 错误 | 含义 | 调用方应对 |
| --- | --- | --- |
| `ErrNotOwner` | 锁不再属于本 Locker（已过期/被他人获取，或本就未持有） | 立即停止临界区写操作；不要重试解锁 |
| `ErrAlreadyHeld` | 同一个 `*Locker` 已经持锁 | 检查是否复用了 Locker 实例 |
| `ErrInvalidKey` / `ErrInvalidTTL` | 构造参数非法 | 修正参数 |
| `redis.ErrNotInitialized` | 尚未 `redis.Init` | 在应用入口补上 `redis.Init` |
| `redis.Nil` / 网络错误 | 见下 | — |

关键行为：

- **`Unlock` 遇到网络错误时不会清除本地 `held`**（所有权未知），调用方可以重试释放；
  只有「令牌不匹配」（脚本返回 0）才判定为 `ErrNotOwner` 并清除状态。
- **`Renew` 遇到令牌不匹配**同样判定锁已丢失并清除本地状态。
- **`Verify` 是只读的**，发现不匹配时也会清除本地状态（因为已经确认丢失）。
- **`WithLock` 释放失败不会掩盖 `fn` 的错误**；锁最终会随 TTL 自动过期。

## 性能分析

单次加锁/解锁的固定开销（实测，Windows/amd64，`-benchmem`）：

| 基准 | 结果 | 分配 | 说明 |
| --- | --- | --- | --- |
| `NewToken` | 107 ns/op | 32 B / 1 alloc | 16 字节 CSPRNG + hex 编码，每次 `TryLock` 一次 |
| `Jitter` | 6.8 ns/op | 0 alloc | 重试抖动计算，可忽略 |
| `redisInt64` | 1.6 ns/op | 0 alloc | Lua 返回值类型归一 |

真实路径成本构成：

| 操作 | Redis 命令 | 额外开销 |
| --- | --- | --- |
| `TryLock` | `SET key token NX PX ttl`（1 RTT） | 令牌生成 + `redis.Default()` 解析（纳秒级） |
| `Unlock` | `EVAL releaseScript`（1 RTT） | 脚本首次执行走 EVAL（非 EVALSHA），见下 |
| `Renew` | `EVAL renewScript`（1 RTT） | 同上 |
| `Verify` | `GET`（1 RTT） | 无 |
| `WithLock` | 加锁 + 解锁 = 2 RTT | — |

## 注意事项（分布式锁的固有边界）

1. **没有自动续期（watchdog）**：长任务的锁会在 TTL 到期后自动释放。请自行按
   `ttl/3` 左右周期调用 `Renew`，并在 `Renew` 返回 `ErrNotOwner` 时立即中止写操作。
2. **`IsHeld()` 只是本地状态**：进程暂停（GC/CPU 抢占）、网络分区都可能让锁在 Redis
   中已过期。对一致性敏感的操作，**在关键写入前用 `Verify` 确认**，或使用 fencing token
   在下游存储层做单调性校验。
3. **Redlock 争议**：本实现是「单实例/主从」租约锁，不提供 Redlock 的多数派保证。
   主从切换（异步复制）期间可能出现两个持有者；对强一致场景需配合 fencing token。
4. **令牌匹配只保证「不误删」**：它无法阻止「锁过期后原持有者仍在写」的情况。
5. **`WithLock` 的回调必须尊重 `ctx`**：加锁成功后若回调长时间不返回，锁会随 TTL 过期。
6. **不要跨 goroutine 共享同一个 Locker 做不同临界区**，虽然内部有互斥保护，
   但语义上会把两段逻辑串行化并可能返回 `ErrAlreadyHeld`。

## 测试

```bash
# 单元测试（无需 Redis）
go test ./lock/

# 基准
go test ./lock/ -run '^$' -bench . -benchmem

# 集成测试（加锁/续期/自检/释放/二次释放）
REDIS_ADDR=127.0.0.1:6379 go test ./lock/ -run TestLockIntegration -v
```
