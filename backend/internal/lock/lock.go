package lock

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Locker 基于 Redis SET NX EX + Lua 脚本的分布式锁。
// 锁 key 必须由 Worker 在拿到业务消息后以 stream+messageID 派生,这里只提供操作能力。
type Locker struct {
	client *redis.Client
}

// New 用同一个 go-redis 客户端构造 Locker
func New(client *redis.Client) *Locker { return &Locker{client: client} }

// Key 标准锁 key 派生:lock:stream:{stream}:message:{messageID}
func Key(stream, messageID string) string {
	return fmt.Sprintf("lock:stream:%s:message:%s", stream, messageID)
}

// Acquire SET key value NX EX ttl。成功返回 true。
func (l *Locker) Acquire(ctx context.Context, key, consumerName string, ttl time.Duration) (bool, error) {
	ok, err := l.client.SetNX(ctx, key, consumerName, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Release Lua 校验持有者一致后再 DEL。
func (l *Locker) Release(ctx context.Context, key, consumerName string) error {
	script := `if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end`
	_, err := l.client.Eval(ctx, script, []string{key}, consumerName).Result()
	if err == redis.Nil {
		return nil
	}
	return err
}

// Renew Lua 校验持有者一致后续期 TTL。
func (l *Locker) Renew(ctx context.Context, key, consumerName string, ttl time.Duration) (bool, error) {
	script := `if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("expire", KEYS[1], ARGV[2])
else
  return 0
end`
	v, err := l.client.Eval(ctx, script, []string{key}, consumerName, int(ttl.Seconds())).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	n, _ := v.(int64)
	return n == 1, nil
}
