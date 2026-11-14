package database

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// IsRetryable 判断错误是否为可重做整个事务的并发冲突：
// MySQL 死锁（1213）、锁等待超时（1205）；PostgreSQL 序列化失败（40001）、死锁（40P01）。
func IsRetryable(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1213 || me.Number == 1205
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "40001" || pe.Code == "40P01"
	}
	return false
}

// RetryBackoff 第 n 次（从 1 开始）冲突后重做前的等待时间，带随机抖动避免多个事务同时重做再次冲突。
var RetryBackoff = func(n int) time.Duration {
	return time.Duration(n)*20*time.Millisecond + rand.N(30*time.Millisecond)
}

// XactLock 在事务内获取命名互斥锁，其它持有同名锁的事务（包括其它实例）需等待；返回的 unlock 必须在事务结束前调用。
// MySQL 使用 GET_LOCK（整个 MySQL 服务共享锁名，因此按库名区分），PostgreSQL 使用事务级咨询锁（事务结束自动释放）。
func XactLock(tx *gorm.DB, name string, timeout time.Duration) (unlock func(), err error) {
	sum := sha1.Sum([]byte(tx.Migrator().CurrentDatabase() + ":" + name))
	key := "gvb:" + hex.EncodeToString(sum[:])
	if IsPostgres(tx) {
		return func() {}, tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error
	}
	var got sql.NullInt64
	if err := tx.Raw("SELECT GET_LOCK(?, ?)", key, int(timeout.Seconds())).Scan(&got).Error; err != nil {
		return nil, err
	}
	if !got.Valid || got.Int64 != 1 {
		return nil, fmt.Errorf("等待数据库锁 %s 超时", name)
	}
	return func() { tx.Exec("SELECT RELEASE_LOCK(?)", key) }, nil
}

// Transaction 执行事务，遇到可重做的并发冲突时整体回滚后重做，最多执行 attempts 次。
// fn 必须可以安全地重复执行（事务回滚后不留下任何副作用）。
func Transaction(ctx context.Context, db *gorm.DB, attempts int, fn func(tx *gorm.DB) error) error {
	for n := 1; ; n++ {
		err := db.WithContext(ctx).Transaction(fn)
		if err == nil || n >= attempts || !IsRetryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(RetryBackoff(n)):
		}
	}
}
