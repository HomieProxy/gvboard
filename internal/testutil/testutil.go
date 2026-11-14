// Package testutil 集成测试公共工具，只能在测试代码中使用。
//
// 通过环境变量启用，未设置时相应测试自动跳过：
//
//	GVB_TEST_MYSQL_DSN  例如 root:password@tcp(127.0.0.1:3306)/?charset=utf8mb4&parseTime=true
//	GVB_TEST_PG_DSN     例如 postgres://postgres:password@127.0.0.1:5432/postgres?sslmode=disable
//	GVB_TEST_REDIS_ADDR 例如 127.0.0.1:6379
package testutil

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/migrate"
)

var seq atomic.Int64

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d_%d_%d", prefix, time.Now().UnixNano(), os.Getpid(), seq.Add(1))
}

// Drivers 返回已配置的数据库驱动。
func Drivers() []string {
	var out []string
	if os.Getenv("GVB_TEST_MYSQL_DSN") != "" {
		out = append(out, config.DriverMySQL)
	}
	if os.Getenv("GVB_TEST_PG_DSN") != "" {
		out = append(out, config.DriverPostgres)
	}
	return out
}

// DB 创建临时数据库并执行全部迁移（全新安装结构），测试结束后删除。
func DB(t testing.TB, driver string) *gorm.DB {
	t.Helper()
	name := uniqueName("gvb_test_")
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var db *gorm.DB
	switch driver {
	case config.DriverMySQL:
		dsn := os.Getenv("GVB_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("未设置 GVB_TEST_MYSQL_DSN，跳过")
		}
		admin, err := gorm.Open(mysql.Open(dsn), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4").Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP DATABASE " + name) })
		if db, err = gorm.Open(mysql.Open(strings.Replace(dsn, "/?", "/"+name+"?", 1)), cfg); err != nil {
			t.Fatal(err)
		}
	case config.DriverPostgres:
		dsn := os.Getenv("GVB_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("未设置 GVB_TEST_PG_DSN，跳过")
		}
		admin, err := gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") })
		i := strings.LastIndex(dsn, "/")
		rest := ""
		if j := strings.Index(dsn[i:], "?"); j >= 0 {
			rest = dsn[i+j:]
		}
		if db, err = gorm.Open(postgres.Open(dsn[:i+1]+name+rest), cfg); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("未知驱动 %s", driver)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if _, err := migrate.New(db, driver).Run(context.Background()); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

// Redis 连接测试 Redis，使用独立前缀，测试结束后删除该前缀下的全部键。
func Redis(t testing.TB) *cache.Cache {
	t.Helper()
	addr := os.Getenv("GVB_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 GVB_TEST_REDIS_ADDR，跳过")
	}
	c, err := cache.Open(config.RedisConfig{Addr: addr, Prefix: uniqueName("gvbtest") + ":"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		iter := c.Scan(ctx, 0, c.Prefix()+"*", 500).Iterator()
		for iter.Next(ctx) {
			c.Del(ctx, iter.Val())
		}
		c.Close()
	})
	return c
}
