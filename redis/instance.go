package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Instance 是一个已初始化 redis 实例的句柄。
//
// 业务通常通过包级函数（redis.Get / redis.Set ...）操作默认实例；需要操作具名
// 实例时，用 redis.Named("xxx") 拿到 *Instance，再调用其上的命令方法。
//
// 所有命令方法都会自动给 key 加上该实例配置的前缀（Config.Prefix）。
type Instance struct {
	name    string                  // 名称
	client  goredis.UniversalClient // go-redis通用客户端
	prefix  string                  // key前缀 用来隔离不同的业务使得海量的key变得可管理
	metrics *metrics                // 监控指标（DisableMetrics 时为 nil）
}

// Name 返回实例名称。
func (i *Instance) Name() string { return i.name }

// Client 返回底层的 go-redis 通用客户端。注意：它返回的客户端不会自动附加前缀，请使用Key()自行拼接。
func (i *Instance) Client() goredis.UniversalClient { return i.client }

// Key 返回加上实例前缀后的完整 key。
func (i *Instance) Key(key string) string { return i.prefix + key }

func (i *Instance) Get(ctx context.Context, key string) (string, error) {
	return i.client.Get(ctx, i.Key(key)).Result()
}
func (i *Instance) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return i.client.Set(ctx, i.Key(key), value, expiration).Err()
}
func (i *Instance) SetNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	return i.client.SetNX(ctx, i.Key(key), value, expiration).Result()
}
func (i *Instance) MGet(ctx context.Context, keys ...string) ([]any, error) {
	prefixed := make([]string, len(keys))
	for n, key := range keys {
		prefixed[n] = i.Key(key)
	}
	return i.client.MGet(ctx, prefixed...).Result()
}
func (i *Instance) MSet(ctx context.Context, values ...any) error {
	if len(values)%2 != 0 {
		return fmt.Errorf("redis: MSet expects key/value pairs")
	}
	args := make([]any, len(values))
	copy(args, values)
	for n := 0; n < len(args); n += 2 {
		key, ok := args[n].(string)
		if !ok {
			return fmt.Errorf("redis: MSet key at argument %d must be a string", n)
		}
		args[n] = i.Key(key)
	}
	return i.client.MSet(ctx, args...).Err()
}
func (i *Instance) Del(ctx context.Context, keys ...string) (int64, error) {
	return i.client.Del(ctx, i.keys(keys)...).Result()
}
func (i *Instance) Exists(ctx context.Context, keys ...string) (int64, error) {
	return i.client.Exists(ctx, i.keys(keys)...).Result()
}
func (i *Instance) Expire(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	return i.client.Expire(ctx, i.Key(key), expiration).Result()
}
func (i *Instance) TTL(ctx context.Context, key string) (time.Duration, error) {
	return i.client.TTL(ctx, i.Key(key)).Result()
}
func (i *Instance) Incr(ctx context.Context, key string) (int64, error) {
	return i.client.Incr(ctx, i.Key(key)).Result()
}
func (i *Instance) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	return i.client.IncrBy(ctx, i.Key(key), value).Result()
}
func (i *Instance) Decr(ctx context.Context, key string) (int64, error) {
	return i.client.Decr(ctx, i.Key(key)).Result()
}
func (i *Instance) DecrBy(ctx context.Context, key string, value int64) (int64, error) {
	return i.client.DecrBy(ctx, i.Key(key), value).Result()
}

func (i *Instance) HGet(ctx context.Context, key, field string) (string, error) {
	return i.client.HGet(ctx, i.Key(key), field).Result()
}
func (i *Instance) HSet(ctx context.Context, key string, values ...any) error {
	return i.client.HSet(ctx, i.Key(key), values...).Err()
}
func (i *Instance) HMGet(ctx context.Context, key string, fields ...string) ([]any, error) {
	return i.client.HMGet(ctx, i.Key(key), fields...).Result()
}
func (i *Instance) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return i.client.HGetAll(ctx, i.Key(key)).Result()
}
func (i *Instance) HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	return i.client.HDel(ctx, i.Key(key), fields...).Result()
}

func (i *Instance) GetJSON(ctx context.Context, key string, dst any) error {
	value, err := i.Get(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(value), dst)
}
func (i *Instance) SetJSON(ctx context.Context, key string, value any, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return i.Set(ctx, key, data, expiration)
}

func (i *Instance) keys(keys []string) []string {
	out := make([]string, len(keys))
	for n, key := range keys {
		out[n] = i.Key(key)
	}
	return out
}

// Ping 检查连接性。
func (i *Instance) Ping(ctx context.Context) error { return i.client.Ping(ctx).Err() }

// List operations.
func (i *Instance) LPush(ctx context.Context, key string, values ...any) (int64, error) {
	return i.client.LPush(ctx, i.Key(key), values...).Result()
}
func (i *Instance) RPush(ctx context.Context, key string, values ...any) (int64, error) {
	return i.client.RPush(ctx, i.Key(key), values...).Result()
}
func (i *Instance) LPop(ctx context.Context, key string) (string, error) {
	return i.client.LPop(ctx, i.Key(key)).Result()
}
func (i *Instance) RPop(ctx context.Context, key string) (string, error) {
	return i.client.RPop(ctx, i.Key(key)).Result()
}
func (i *Instance) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return i.client.LRange(ctx, i.Key(key), start, stop).Result()
}

// Set operations.
func (i *Instance) SAdd(ctx context.Context, key string, members ...any) (int64, error) {
	return i.client.SAdd(ctx, i.Key(key), members...).Result()
}
func (i *Instance) SRem(ctx context.Context, key string, members ...any) (int64, error) {
	return i.client.SRem(ctx, i.Key(key), members...).Result()
}
func (i *Instance) SMembers(ctx context.Context, key string) ([]string, error) {
	return i.client.SMembers(ctx, i.Key(key)).Result()
}
func (i *Instance) SIsMember(ctx context.Context, key string, member any) (bool, error) {
	return i.client.SIsMember(ctx, i.Key(key), member).Result()
}

// Sorted-set operations.
func (i *Instance) ZAdd(ctx context.Context, key string, members ...goredis.Z) (int64, error) {
	return i.client.ZAdd(ctx, i.Key(key), members...).Result()
}
func (i *Instance) ZRem(ctx context.Context, key string, members ...any) (int64, error) {
	return i.client.ZRem(ctx, i.Key(key), members...).Result()
}
func (i *Instance) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return i.client.ZRange(ctx, i.Key(key), start, stop).Result()
}
func (i *Instance) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return i.client.ZRevRange(ctx, i.Key(key), start, stop).Result()
}
func (i *Instance) ZScore(ctx context.Context, key string, member string) (float64, error) {
	return i.client.ZScore(ctx, i.Key(key), member).Result()
}

func (i *Instance) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return i.client.Eval(ctx, script, i.keys(keys), args...).Result()
}
func (i *Instance) EvalSha(ctx context.Context, sha1 string, keys []string, args ...any) (any, error) {
	return i.client.EvalSha(ctx, sha1, i.keys(keys), args...).Result()
}

func (i *Instance) Publish(ctx context.Context, channel string, message any) (int64, error) {
	return i.client.Publish(ctx, i.Key(channel), message).Result()
}
func (i *Instance) Subscribe(ctx context.Context, channels ...string) *goredis.PubSub {
	return i.client.Subscribe(ctx, i.keys(channels)...)
}
