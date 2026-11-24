// Package redis provides a small, zero-injection facade over go-redis/v9.
// Initialize it once during application startup and close it after workers stop.
package redis

import (
	"context"
	goredis "github.com/redis/go-redis/v9"
	"time"
)

func Get(ctx context.Context, key string) (string, error) {
	i, e := Default()
	if e != nil {
		return "", e
	}
	return i.Get(ctx, key)
}
func Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	i, e := Default()
	if e != nil {
		return e
	}
	return i.Set(ctx, key, value, expiration)
}
func SetNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	i, e := Default()
	if e != nil {
		return false, e
	}
	return i.SetNX(ctx, key, value, expiration)
}
func MGet(ctx context.Context, keys ...string) ([]any, error) {
	i, e := Default()
	if e != nil {
		return nil, e
	}
	return i.MGet(ctx, keys...)
}
func MSet(ctx context.Context, values ...any) error {
	i, e := Default()
	if e != nil {
		return e
	}
	return i.MSet(ctx, values...)
}
func Del(ctx context.Context, keys ...string) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.Del(ctx, keys...)
}
func Exists(ctx context.Context, keys ...string) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.Exists(ctx, keys...)
}
func Expire(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	i, e := Default()
	if e != nil {
		return false, e
	}
	return i.Expire(ctx, key, expiration)
}
func TTL(ctx context.Context, key string) (time.Duration, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.TTL(ctx, key)
}
func Incr(ctx context.Context, key string) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.Incr(ctx, key)
}
func IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.IncrBy(ctx, key, value)
}
func Decr(ctx context.Context, key string) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.Decr(ctx, key)
}
func DecrBy(ctx context.Context, key string, value int64) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.DecrBy(ctx, key, value)
}
func HGet(ctx context.Context, key, field string) (string, error) {
	i, e := Default()
	if e != nil {
		return "", e
	}
	return i.HGet(ctx, key, field)
}
func HSet(ctx context.Context, key string, values ...any) error {
	i, e := Default()
	if e != nil {
		return e
	}
	return i.HSet(ctx, key, values...)
}
func HMGet(ctx context.Context, key string, fields ...string) ([]any, error) {
	i, e := Default()
	if e != nil {
		return nil, e
	}
	return i.HMGet(ctx, key, fields...)
}
func HGetAll(ctx context.Context, key string) (map[string]string, error) {
	i, e := Default()
	if e != nil {
		return nil, e
	}
	return i.HGetAll(ctx, key)
}
func HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	i, e := Default()
	if e != nil {
		return 0, e
	}
	return i.HDel(ctx, key, fields...)
}
func GetJSON(ctx context.Context, key string, dst any) error {
	i, e := Default()
	if e != nil {
		return e
	}
	return i.GetJSON(ctx, key, dst)
}
func SetJSON(ctx context.Context, key string, value any, expiration time.Duration) error {
	i, e := Default()
	if e != nil {
		return e
	}
	return i.SetJSON(ctx, key, value, expiration)
}

// List commands.
func LPush(ctx context.Context, key string, values ...any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.LPush(ctx, key, values...)
}
func RPush(ctx context.Context, key string, values ...any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.RPush(ctx, key, values...)
}
func LPop(ctx context.Context, key string) (string, error) {
	i, err := Default()
	if err != nil {
		return "", err
	}
	return i.LPop(ctx, key)
}
func RPop(ctx context.Context, key string) (string, error) {
	i, err := Default()
	if err != nil {
		return "", err
	}
	return i.RPop(ctx, key)
}
func LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.LRange(ctx, key, start, stop)
}

// Set commands.
func SAdd(ctx context.Context, key string, members ...any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.SAdd(ctx, key, members...)
}
func SRem(ctx context.Context, key string, members ...any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.SRem(ctx, key, members...)
}
func SMembers(ctx context.Context, key string) ([]string, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.SMembers(ctx, key)
}
func SIsMember(ctx context.Context, key string, member any) (bool, error) {
	i, err := Default()
	if err != nil {
		return false, err
	}
	return i.SIsMember(ctx, key, member)
}

// Sorted-set commands.
func ZAdd(ctx context.Context, key string, members ...goredis.Z) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.ZAdd(ctx, key, members...)
}
func ZRem(ctx context.Context, key string, members ...any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.ZRem(ctx, key, members...)
}
func ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.ZRange(ctx, key, start, stop)
}
func ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.ZRevRange(ctx, key, start, stop)
}
func ZScore(ctx context.Context, key, member string) (float64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.ZScore(ctx, key, member)
}

// Lua and PubSub commands. Channel names use the configured key prefix too.
func Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.Eval(ctx, script, keys, args...)
}
func EvalSha(ctx context.Context, sha1 string, keys []string, args ...any) (any, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.EvalSha(ctx, sha1, keys, args...)
}
func Publish(ctx context.Context, channel string, message any) (int64, error) {
	i, err := Default()
	if err != nil {
		return 0, err
	}
	return i.Publish(ctx, channel, message)
}
func Subscribe(ctx context.Context, channels ...string) (*goredis.PubSub, error) {
	i, err := Default()
	if err != nil {
		return nil, err
	}
	return i.Subscribe(ctx, channels...), nil
}
