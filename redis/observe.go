package redis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// metrics 是一个零依赖、低开销的 Redis 命令观测器。
//
// 之所以不导出：它只作为 go-redis Hook 的内部实现存在。业务侧若需要指标，
// 统一通过 redis.MetricsHandler() 暴露 Prometheus 文本；若需要自定义观测，
// 应自行实现 goredis.Hook 并通过 Config.Hooks 注入（见 client.go）。
//
// 设计上刻意用「固定命令标签 + 固定延迟桶」来约束基数：未知命令统一归入
// "other"，桶边界来自包级 latencyBuckets，因此标签组合数量有上界。
type metrics struct {
	commands atomic.Uint64
	errors   atomic.Uint64
	inflight atomic.Int64
	byCmd    sync.Map // map[string]*commandMetric；命令集合有界
	buckets  []time.Duration
}

type commandMetric struct {
	count   atomic.Uint64
	errors  atomic.Uint64
	totalNS atomic.Uint64
	le      []atomic.Uint64 // 长度 = len(buckets)+1，最后一个是 +Inf
}

var latencyBuckets = []time.Duration{
	time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second,
}

func newMetrics() *metrics {
	return &metrics{buckets: append([]time.Duration(nil), latencyBuckets...)}
}

func (m *metrics) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (m *metrics) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		start := time.Now()
		m.inflight.Add(1)
		err := next(ctx, cmd)
		m.inflight.Add(-1)
		m.observe(cmd.Name(), time.Since(start), err)
		return err
	}
}

func (m *metrics) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		start := time.Now()
		m.inflight.Add(1)
		err := next(ctx, cmds)
		m.inflight.Add(-1)
		// pipeline 是一次往返，无法观测单条命令延迟，因此整体记为 "pipeline"。
		m.observe("pipeline", time.Since(start), err)
		return err
	}
}

func (m *metrics) observe(name string, d time.Duration, err error) {
	name = metricCommand(name)
	m.commands.Add(1)
	isError := err != nil && !errors.Is(err, goredis.Nil)
	if isError {
		m.errors.Add(1)
	}

	// 先 Load 再在未命中时 LoadOrStore。
	// 不能直接 LoadOrStore(name, &commandMetric{...})：函数实参会在调用前求值，
	// 即使 key 已存在，那个 &commandMetric{le: make(...)} 也会被分配并随即丢弃，
	// 导致每条命令固定多出 2 次堆分配（结构体 + 桶数组）。
	vAny, ok := m.byCmd.Load(name)
	if !ok {
		vAny, _ = m.byCmd.LoadOrStore(name, &commandMetric{le: make([]atomic.Uint64, len(m.buckets)+1)})
	}
	v := vAny.(*commandMetric)

	v.count.Add(1)
	if d > 0 {
		v.totalNS.Add(uint64(d))
	}
	if isError {
		v.errors.Add(1)
	}
	v.le[observeBucket(m.buckets, d)].Add(1)
}

// observeBucket 返回第一个满足 d <= buckets[i] 的下标；
// 若 d 超过所有边界则返回 len(buckets)（即 +Inf 桶）。
//
// 这里手写二分而不是 sort.Search：sort.Search 接收闭包，闭包捕获 d/buckets 会逃逸到堆，
// 在每条命令的热路径上产生一次额外分配。
func observeBucket(buckets []time.Duration, d time.Duration) int {
	lo, hi := 0, len(buckets)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if d <= buckets[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// metricCommand 把命令名收敛到有界集合，避免自定义命令导致指标基数爆炸。
func metricCommand(s string) string {
	s = strings.ToLower(s)
	switch s {
	case "get", "set", "setnx", "mget", "mset", "del", "exists", "expire", "ttl",
		"incr", "decr", "hget", "hset", "hmget", "hgetall", "hdel",
		"lpush", "rpush", "lpop", "rpop", "lrange",
		"sadd", "srem", "smembers", "sismember",
		"zadd", "zrem", "zrange", "zrevrange", "zscore",
		"eval", "evalsha", "publish", "subscribe", "ping", "pipeline",
		"client", "auth", "hello", "select", "multi", "exec", "discard",
		"scan", "unlink", "msetnx", "getdel", "getex", "setex", "psetex",
		"expireat", "pexpire", "pexpireat", "pttl", "flushdb", "info", "command":
		return s
	default:
		return "other"
	}
}

// metricsSnapshot 是不可变的指标快照，用于日志/监控。
type metricsSnapshot struct {
	Commands, Errors uint64
	InFlight         int64
	Buckets          []time.Duration // 与各命令的 Buckets 一一对应（+Inf 桶单独追加）
	ByCommand        map[string]commandSnapshot
}

type commandSnapshot struct {
	Count, Errors uint64
	Total         time.Duration
	Buckets       []uint64 // 长度 = len(metricsSnapshot.Buckets)+1
}

func (m *metrics) snapshot() metricsSnapshot {
	if m == nil {
		return metricsSnapshot{}
	}
	s := metricsSnapshot{
		Commands: m.commands.Load(),
		Errors:   m.errors.Load(),
		InFlight: m.inflight.Load(),
		// 快照携带桶边界，保证导出端使用的边界与计数时完全一致；
		// 避免依赖包级 latencyBuckets 与实例副本长度相同这一隐式假设
		// （一旦二者不一致会直接下标越界 panic）。
		Buckets:   append([]time.Duration(nil), m.buckets...),
		ByCommand: make(map[string]commandSnapshot),
	}
	m.byCmd.Range(func(key, value any) bool {
		v := value.(*commandMetric)
		buckets := make([]uint64, len(v.le))
		for n := range v.le {
			buckets[n] = v.le[n].Load()
		}
		s.ByCommand[key.(string)] = commandSnapshot{
			Count:   v.count.Load(),
			Errors:  v.errors.Load(),
			Total:   time.Duration(v.totalNS.Load()),
			Buckets: buckets,
		}
		return true
	})
	return s
}

// MetricsHandler 以 Prometheus 文本格式导出当前所有实例的指标。
//
// 用法：
//
//	mux := http.NewServeMux()
//	mux.Handle("/metrics", redis.MetricsHandler())
//
// 指标包含命令计数/错误数/延迟直方图、在途命令数，以及连接池的
// 连接数、超时、命中/未命中。
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		instances := global.list()

		fmt.Fprintln(w, "# HELP fans_redis_commands_total Redis commands observed.")
		fmt.Fprintln(w, "# TYPE fans_redis_commands_total counter")
		fmt.Fprintln(w, "# HELP fans_redis_command_errors_total Redis command errors observed.")
		fmt.Fprintln(w, "# TYPE fans_redis_command_errors_total counter")
		fmt.Fprintln(w, "# HELP fans_redis_command_duration_seconds Redis command latency histogram.")
		fmt.Fprintln(w, "# TYPE fans_redis_command_duration_seconds histogram")
		fmt.Fprintln(w, "# HELP fans_redis_inflight_commands Current in-flight Redis commands.")
		fmt.Fprintln(w, "# TYPE fans_redis_inflight_commands gauge")
		fmt.Fprintln(w, "# HELP fans_redis_pool_connections Redis connection pool connections.")
		fmt.Fprintln(w, "# TYPE fans_redis_pool_connections gauge")
		fmt.Fprintln(w, "# HELP fans_redis_pool_timeouts_total Redis connection pool timeouts.")
		fmt.Fprintln(w, "# TYPE fans_redis_pool_timeouts_total counter")
		fmt.Fprintln(w, "# HELP fans_redis_pool_hits_total Redis connection pool hits.")
		fmt.Fprintln(w, "# TYPE fans_redis_pool_hits_total counter")
		fmt.Fprintln(w, "# HELP fans_redis_pool_misses_total Redis connection pool misses.")
		fmt.Fprintln(w, "# TYPE fans_redis_pool_misses_total counter")

		for _, inst := range instances {
			snap := inst.metrics.snapshot()

			fmt.Fprintf(w, "fans_redis_inflight_commands{instance=%q} %d\n", inst.Name(), snap.InFlight)

			pool := inst.poolSnapshot()
			fmt.Fprintf(w, "fans_redis_pool_connections{instance=%q,state=\"total\"} %d\n", inst.Name(), pool.TotalConns)
			fmt.Fprintf(w, "fans_redis_pool_connections{instance=%q,state=\"idle\"} %d\n", inst.Name(), pool.IdleConns)
			fmt.Fprintf(w, "fans_redis_pool_timeouts_total{instance=%q} %d\n", inst.Name(), pool.Timeouts)
			fmt.Fprintf(w, "fans_redis_pool_hits_total{instance=%q} %d\n", inst.Name(), pool.Hits)
			fmt.Fprintf(w, "fans_redis_pool_misses_total{instance=%q} %d\n", inst.Name(), pool.Misses)

			for cmd, v := range snap.ByCommand {
				fmt.Fprintf(w, "fans_redis_commands_total{instance=%q,command=%q} %d\n", inst.Name(), cmd, v.Count)
				fmt.Fprintf(w, "fans_redis_command_errors_total{instance=%q,command=%q} %d\n", inst.Name(), cmd, v.Errors)

				var cumulative uint64
				for n, b := range snap.Buckets {
					if n >= len(v.Buckets) {
						break
					}
					cumulative += v.Buckets[n]
					fmt.Fprintf(w, "fans_redis_command_duration_seconds_bucket{instance=%q,command=%q,le=%q} %d\n",
						inst.Name(), cmd, strconv.FormatFloat(b.Seconds(), 'f', -1, 64), cumulative)
				}
				// 最后一个桶是 +Inf。
				if len(v.Buckets) > len(snap.Buckets) {
					cumulative += v.Buckets[len(snap.Buckets)]
				}
				fmt.Fprintf(w, "fans_redis_command_duration_seconds_bucket{instance=%q,command=%q,le=\"+Inf\"} %d\n",
					inst.Name(), cmd, cumulative)
				fmt.Fprintf(w, "fans_redis_command_duration_seconds_sum{instance=%q,command=%q} %f\n",
					inst.Name(), cmd, v.Total.Seconds())
				fmt.Fprintf(w, "fans_redis_command_duration_seconds_count{instance=%q,command=%q} %d\n",
					inst.Name(), cmd, v.Count)
			}
		}
	})
}

// poolSnapshot 是连接池统计的点位快照（内部使用，避免对外暴露 go-redis 类型）。
type poolSnapshot struct {
	Hits, Misses, Timeouts            uint32
	TotalConns, IdleConns, StaleConns uint32
}

func (i *Instance) poolSnapshot() poolSnapshot {
	if i == nil || i.client == nil {
		return poolSnapshot{}
	}
	p := i.client.PoolStats()
	if p == nil {
		return poolSnapshot{}
	}
	return poolSnapshot{
		Hits: p.Hits, Misses: p.Misses, Timeouts: p.Timeouts,
		TotalConns: p.TotalConns, IdleConns: p.IdleConns, StaleConns: p.StaleConns,
	}
}
