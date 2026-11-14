package database_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"gvboard/internal/config"
	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&mysql.MySQLError{Number: 1213}, true},
		{&mysql.MySQLError{Number: 1205}, true},
		{&mysql.MySQLError{Number: 1062}, false},
		{&pgconn.PgError{Code: "40001"}, true},
		{&pgconn.PgError{Code: "40P01"}, true},
		{&pgconn.PgError{Code: "23505"}, false},
		{fmt.Errorf("写入失败: %w", &mysql.MySQLError{Number: 1213}), true},
		{errors.New("deadlock"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := database.IsRetryable(c.err); got != c.want {
			t.Errorf("IsRetryable(%v) = %v，应为 %v", c.err, got, c.want)
		}
	}
}

func TestXactLock(t *testing.T) {
	for _, driver := range testutil.Drivers() {
		t.Run(driver, func(t *testing.T) {
			db := testutil.DB(t, driver)
			holding := make(chan struct{})
			release := make(chan struct{})
			acquired := make(chan time.Time, 1)
			errs := make(chan error, 2)
			go func() {
				errs <- db.Transaction(func(tx *gorm.DB) error {
					unlock, err := database.XactLock(tx, "test", 5*time.Second)
					if err != nil {
						return err
					}
					defer unlock()
					close(holding)
					<-release
					return nil
				})
			}()
			<-holding
			go func() {
				errs <- db.Transaction(func(tx *gorm.DB) error {
					unlock, err := database.XactLock(tx, "test", 5*time.Second)
					if err != nil {
						return err
					}
					acquired <- time.Now()
					unlock()
					return nil
				})
			}()
			select {
			case <-acquired:
				t.Fatal("锁被占用时不应获取成功")
			case <-time.After(300 * time.Millisecond):
			}
			released := time.Now()
			close(release)
			select {
			case at := <-acquired:
				if at.Before(released) {
					t.Fatal("应在释放后才获取到锁")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("释放后应能获取到锁")
			}
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTransactionRetry(t *testing.T) {
	old := database.RetryBackoff
	database.RetryBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { database.RetryBackoff = old })
	deadlock := map[string]error{
		config.DriverMySQL:    &mysql.MySQLError{Number: 1213},
		config.DriverPostgres: &pgconn.PgError{Code: "40P01"},
	}
	for _, driver := range testutil.Drivers() {
		t.Run(driver, func(t *testing.T) {
			db := testutil.DB(t, driver)
			ctx := context.Background()
			count := func(name string) int64 {
				var n int64
				db.Model(&model.ServerGroup{}).Where("name = ?", name).Count(&n)
				return n
			}
			run := func(name string, attempts int, fail func(n int) error) (int, error) {
				calls := 0
				err := database.Transaction(ctx, db, attempts, func(tx *gorm.DB) error {
					calls++
					if err := tx.Create(&model.ServerGroup{Name: name}).Error; err != nil {
						return err
					}
					return fail(calls)
				})
				return calls, err
			}

			calls, err := run("retry-ok", 5, func(n int) error {
				if n < 3 {
					return deadlock[driver]
				}
				return nil
			})
			if err != nil || calls != 3 || count("retry-ok") != 1 {
				t.Errorf("前两次死锁应回滚并重做，第三次成功：调用 %d 次，%d 行，%v", calls, count("retry-ok"), err)
			}

			calls, err = run("retry-max", 3, func(int) error { return deadlock[driver] })
			if !database.IsRetryable(err) || calls != 3 || count("retry-max") != 0 {
				t.Errorf("达到次数上限应返回最后的错误且不留数据：调用 %d 次，%d 行，%v", calls, count("retry-max"), err)
			}

			other := errors.New("业务错误")
			calls, err = run("retry-other", 5, func(int) error { return other })
			if !errors.Is(err, other) || calls != 1 || count("retry-other") != 0 {
				t.Errorf("非并发冲突的错误不应重做：调用 %d 次，%v", calls, err)
			}
		})
	}
}
