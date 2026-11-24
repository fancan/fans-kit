// Package cache 是构建在 redis 之上的轻量级 JSON 缓存。
//
//	 使用eg:
//		redis.Init(cfg)
//		...
//		cache.Remember(ctx, "pool:0xabc", time.Minute, &v, loader)
//		...
//		redis.Close()
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	kitredis "github.com/fancan/fans-kit/redis"
)

var (
	ErrNilLoader   = errors.New("cache: nil loader")
	ErrLoaderPanic = errors.New("cache: loader panicked")
)

// flight 是一次进行中的加载。
type flight struct {
	done chan struct{}
	data []byte
	err  error
}

// 进程内 singleflight 状态：同一 key 的并发加载合并为一次。
// 这里刻意做成包级共享，而不是每次调用新建，否则合并会完全失效。
var (
	flightMu sync.Mutex
	flights  map[string]*flight
)

// getRedisInstance 返回每次锁操作使用的 redis 实例 注意：这里返回的是默认的，实际多业务线这里需要稍微改造一下匹配到自己业务线的
func getRedisInstance() (*kitredis.Instance, error) {
	return kitredis.Default()
}

// Get 读取默认 redis 实例上 key 的 JSON 数据并反序列化到 dst。
// key 不存在时返回 kitredis.Nil。
func Get(ctx context.Context, key string, dst any) error {
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}
	return inst.GetJSON(ctx, key, dst)
}

// Set 以 JSON 形式向默认 redis 实例写入 key，并设置过期时间。
// ttl 为 0 表示永不过期。
func Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}
	return inst.SetJSON(ctx, key, value, ttl)
}

// Delete 删除默认 redis 实例上的 key（不区分 key 是否存在）。
func Delete(ctx context.Context, key string) error {
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}
	_, err = inst.Del(ctx, key)
	return err
}

// Remember 读取缓存；未命中时调用 loader 加载并回填缓存。
// 注意：同一进程内针对同一 key 的并发调用会被合并为一次 loader 执行，其余调用者等待并共享结果——用于热点数据防击穿。
func Remember(ctx context.Context, key string, ttl time.Duration, dst any, loader func(context.Context) (any, error)) error {
	if loader == nil {
		return ErrNilLoader
	}
	inst, err := getRedisInstance()
	if err != nil {
		return err
	}

	if err := inst.GetJSON(ctx, key, dst); err == nil {
		return nil
	} else if !errors.Is(err, kitredis.Nil) {
		return err
	}

	flightMu.Lock()
	if flights == nil {
		flights = make(map[string]*flight)
	}
	if f := flights[key]; f != nil {
		flightMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			if f.err != nil {
				return f.err
			}
			return json.Unmarshal(f.data, dst)
		}
	}
	f := &flight{done: make(chan struct{})}
	flights[key] = f
	flightMu.Unlock()

	runFlight(ctx, inst, key, ttl, f, loader)

	if f.err != nil {
		return f.err
	}
	return json.Unmarshal(f.data, dst)
}

// runFlight 执行一次合并后的加载，并把结果发布给所有等待者。
func runFlight(ctx context.Context, inst *kitredis.Instance, key string, ttl time.Duration, f *flight, loader func(context.Context) (any, error)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			f.err = fmt.Errorf("%w: %v", ErrLoaderPanic, recovered)
		}

		flightMu.Lock()
		delete(flights, key)
		close(f.done)
		flightMu.Unlock()
	}()

	loadCtx := context.WithoutCancel(ctx)

	value, loadErr := loader(loadCtx)
	if loadErr != nil {
		f.err = loadErr
		return
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		f.err = marshalErr
		return
	}
	f.data = data

	_ = inst.Set(loadCtx, key, data, ttl)
}
