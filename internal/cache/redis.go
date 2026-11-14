// Package cache 封装 Redis 连接。
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"gvboard/internal/config"
)

// Cache 带键前缀的 Redis 客户端。
type Cache struct {
	*redis.Client
	prefix string
}

// Open 建立 Redis 连接并校验连通性。
func Open(c config.RedisConfig) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     c.Addr,
		Password: c.Password,
		DB:       c.DB,
		PoolSize: c.PoolSize,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("Redis 不可达: %w", err)
	}
	return &Cache{Client: client, prefix: c.Prefix}, nil
}

// Key 为业务键加上全局前缀。所有业务代码访问 Redis 时都应通过此方法生成键。
func (c *Cache) Key(k string) string { return c.prefix + k }

// Prefix 返回全局键前缀。
func (c *Cache) Prefix() string { return c.prefix }
