// Package database 负责建立 MySQL / PostgreSQL 连接。
package database

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gvboard/internal/config"
)

// DSN 根据配置生成连接串。
func DSN(c config.DatabaseConfig) (string, error) {
	switch c.Driver {
	case config.DriverMySQL:
		// loc=Local 与原项目一致：时间字段均为 int 时间戳，此参数仅影响 DATETIME 类型。
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=true&loc=Local&timeout=10s",
			c.User, c.Password, c.Host, c.Port, c.Name, c.Charset), nil
	case config.DriverPostgres:
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(c.User, c.Password),
			Host:     fmt.Sprintf("%s:%d", c.Host, c.Port),
			Path:     "/" + c.Name,
			RawQuery: url.Values{"sslmode": {c.SSLMode}, "connect_timeout": {"10"}}.Encode(),
		}
		return u.String(), nil
	}
	return "", fmt.Errorf("不支持的数据库驱动: %s", c.Driver)
}

// Open 打开数据库连接并校验连通性。
func Open(c config.DatabaseConfig, log *zap.Logger, debug bool) (*gorm.DB, error) {
	dsn, err := DSN(c)
	if err != nil {
		return nil, err
	}
	var dialector gorm.Dialector
	if c.Driver == config.DriverPostgres {
		dialector = postgres.Open(dsn)
	} else {
		dialector = mysql.Open(dsn)
	}

	level := gormlogger.Warn
	if debug {
		level = gormlogger.Info
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:                                   newGormLogger(log, level, c.SlowThreshold),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(c.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("数据库不可达: %w", err)
	}
	return db, nil
}

// Close 关闭数据库连接。
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
