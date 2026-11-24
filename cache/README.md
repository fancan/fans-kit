# Cache —— 基于 Redis 的 JSON 缓存（带防击穿合并加载）

`cache` 是构建在 `fans-kit/redis` 之上的轻量 JSON 缓存层，核心能力是 **`Remember`：
读缓存 / 未命中则加载并回填，且同进程内同一 key 的并发加载会被合并成一次**（singleflight），
用于热点数据防击穿。

## 特性

| 能力 | 说明 |
| --- | --- |
| 零侵入 | 应用入口 `redis.Init` 之后直接 `cache.Get/Set/Delete/Remember`，无需单独初始化或注入实例 |
| JSON 编解码 | 业务只关心 struct，序列化由模块完成 |
| 防击穿 | `Remember` 合并同 key 并发加载，一次回源 |
| 读错误不穿透 | 只有 key 不存在才回源，Redis 抖动不会打穿下游 |
| 回填失败不致命 | 缓存写失败仍返回已加载的数据 |
| 连接由 redis 管理 | 底层实例每次调用时经 `redis.Default()` 惰性解析，`redis.Close` 统一释放连接 |

## 生命周期：只初始化 redis，cache 开箱即用

```go
import (
    kitredis "github.com/fancan/fans-kit/redis"
    "github.com/fancan/fans-kit/cache"
)

func main() {
    // 1) 建立 Redis 连接（唯一的初始化入口）
    if err := kitredis.Init(kitredis.Config{
        Addrs:  []string{"127.0.0.1:6379"},
        Prefix: "app:",
    }); err != nil {
        log.Fatal(err)
    }
    defer kitredis.Close()

    // 2) 直接使用，无需 cache.Init / cache.Close
    var snap PoolSnapshot
    if err := cache.Remember(context.Background(), "pool:0xabc", 30*time.Second,
        &snap, loader); err != nil {
        log.Fatal(err)
    }
}
```

- `cache` **没有自己的 `Init`/`Close`**：连接的开/关完全由 `redis` 模块负责。
- 未调用 `redis.Init` 时，包级函数会返回 `redis.ErrNotInitialized`（用
  `errors.Is(err, kitredis.ErrNotInitialized)` 判断）。

## 零侵入用法

```go
ctx := context.Background()

type PoolSnapshot struct {
    Reserve0 string `json:"reserve0"`
    Reserve1 string `json:"reserve1"`
}

// 直接读写
_ = cache.Set(ctx, "pool:0xabc", PoolSnapshot{"1", "2"}, 30*time.Second)

var snap PoolSnapshot
if err := cache.Get(ctx, "pool:0xabc", &snap); errors.Is(err, kitredis.Nil) {
    // key 不存在
}

// 读缓存或回源（推荐）
err := cache.Remember(ctx, "pool:0xabc", 30*time.Second, &snap,
    func(ctx context.Context) (any, error) {
        // 从数据库/链上加载；同 key 并发只会执行一次
        return loadPoolSnapshot(ctx, "0xabc")
    })

// 删除
_ = cache.Delete(ctx, "pool:0xabc")
```

`Remember` 的 loader 返回 `(any, error)`，模块负责 `json.Marshal` 后写回，
并把同一份数据反序列化给所有等待者。

## `Remember` 语义详解

```go
func Remember(ctx context.Context, key string, ttl time.Duration, dst any,
    loader func(context.Context) (any, error)) error
```

执行流程：

1. `GET key` 命中 → 直接反序列化返回；
2. 返回 `redis.Nil`（key 不存在）→ 进入合并加载；
3. 其他读错误（超时/连接失败）→ **直接返回错误，不回源**（保护下游）；
4. 合并加载：
   - 本进程内已有同 key 的进行中加载 → 等待它完成，共享结果（或共享错误）；
   - 否则成为 leader：执行 loader → `Marshal` → `SET key ttl`；
5. 所有等待者用同一份字节反序列化到各自的 `dst`。

关键保证与取舍：

| 场景 | 行为 |
| --- | --- |
| 并发 8 个请求命中同 key 冷缓存 | loader 只执行 1 次，8 个请求共享结果 |
| loader 返回错误 | 所有等待者拿到同一个错误；不写缓存；下次请求会重新加载 |
| loader panic | 被 recover 为 `ErrLoaderPanic`（附带 panic 值），其余同上 |
| `json.Marshal` 失败（如含 chan） | 返回该错误，不写缓存 |
| `SET` 回填失败 | **不影响本次返回**：数据已加载成功，调用方照常拿到结果，只是没缓存上 |
| leader 的 ctx 被取消 | 合并加载与调用方取消**解绑**，其他等待者不受影响；loader 需自行控制耗时 |
| follower 等待超时/取消 | 该 follower 返回 `ctx.Err()`，其他等待者不受影响 |
| `ttl == 0` | 写入后永不过期（请自行确认是否会无限增长） |

> 合并范围是**单个进程内**。多实例部署时，N 个进程仍可能各回源一次；
> 需要跨进程防击穿请在 loader 内叠加分布式锁（见 `fans-kit/lock`）。

## API 一览

| 函数 | 说明 |
| --- | --- |
| `Get(ctx, key, dst)` | 读并反序列化；不存在返回 `kitredis.Nil` |
| `Set(ctx, key, value, ttl)` | 序列化并写入 |
| `Delete(ctx, key)` | 删除 |
| `Remember(ctx, key, ttl, dst, loader)` | 读或回源合并 |

所有函数都作用于 redis 的默认实例（`redis.Default()`），无需（也不提供）实例注入。

## 性能分析

`cache` 自身没有重计算，单次操作的成本构成：

| 路径 | 成本 |
| --- | --- |
| `Get` 命中 | 1 次 Redis GET（RTT）+ `json.Unmarshal` |
| `Set` | `json.Marshal` + 1 次 Redis SET（RTT） |
| `Remember` 命中 | 与 `Get` 相同 |
| `Remember` 未命中（leader） | 1×GET + loader + Marshal + 1×SET |
| `Remember` 未命中（follower） | 1×GET + 等待 channel + Unmarshal |

本模块在 Redis RTT 之外引入的固定开销：

- 合并状态：1 次 `sync.Mutex` 加锁 + 1 次 map 查找/插入 + 1 次 channel 关闭，
  属于 **百纳秒量级**；
- 实例解析：`redis.Default()` 一次读锁 + map 查找，纳秒级，可忽略；
- JSON 编解码：与 payload 大小线性相关，通常是 miss 路径的主要 CPU 成本。

> 端到端基准需要真实 Redis：`REDIS_ADDR=127.0.0.1:6379 go test ./cache/ -run TestRemember -v`。

## 注意事项

1. **`Remember` 的 `dst` 必须是可取地址的指针**，否则 `json.Unmarshal` 报错。
2. **loader 返回值应可 JSON 序列化**；含 `chan`/`func`/循环引用会返回 Marshal 错误。
3. **`ttl == 0` 表示永不过期**，长期写入会导致内存持续增长。
4. **合并只在单进程内生效**，多副本部署不共享。
5. **loader 与调用方取消解绑**，因此 loader 内的慢查询不会因首个请求超时而被中断，
   请用 `context.WithTimeout` 等方式自行兜底。

## 测试

```bash
# 单元测试（无需 Redis）
go test ./cache/

# 集成测试（singleflight 合并验证）
REDIS_ADDR=127.0.0.1:6379 go test ./cache/ -run TestRememberSingleflight -v
```
