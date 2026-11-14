package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// GetJSON 读取 JSON 值。键不存在时返回 false 且不报错。
func (c *Cache) GetJSON(ctx context.Context, key string, v any) (bool, error) {
	b, err := c.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// SetJSON 以 JSON 写入，ttl 为 0 表示不过期。
func (c *Cache) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, b, ttl).Err()
}

// AddJSON 仅当键不存在时写入（对应原项目 Cache::add），返回是否写入成功。
func (c *Cache) AddJSON(ctx context.Context, key string, v any, ttl time.Duration) (bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	return c.SetNX(ctx, key, b, ttl).Result()
}

var unlockScript = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)

// ErrLockHeld 锁已被其他进程持有。
var ErrLockHeld = errors.New("锁已被占用")

// Lock 分布式锁。到期自动释放；Unlock 只会释放自己持有的锁。
type Lock struct {
	c     *Cache
	key   string
	token string
}

// TryLock 尝试获取锁（键会自动加前缀），获取失败返回 ErrLockHeld。
func (c *Cache) TryLock(ctx context.Context, name string, ttl time.Duration) (*Lock, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	l := &Lock{c: c, key: c.Key("lock:" + name), token: hex.EncodeToString(b)}
	ok, err := c.SetNX(ctx, l.key, l.token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLockHeld
	}
	return l, nil
}

// Unlock 释放锁。
func (l *Lock) Unlock(ctx context.Context) error {
	return unlockScript.Run(ctx, l.c.Client, []string{l.key}, l.token).Err()
}

var extendScript = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`)

// Extend 延长自己持有的锁，返回 false 表示锁已过期或被他人持有。
func (l *Lock) Extend(ctx context.Context, ttl time.Duration) (bool, error) {
	n, err := extendScript.Run(ctx, l.c.Client, []string{l.key}, l.token, ttl.Milliseconds()).Int()
	return n == 1, err
}
