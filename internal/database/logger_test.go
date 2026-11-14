package database_test

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestLoggerHidesParams(t *testing.T) {
	const secret = "secret-token-7f3a9c"
	for _, driver := range testutil.Drivers() {
		t.Run(driver, func(t *testing.T) {
			db := testutil.DB(t, driver)
			for _, c := range []struct {
				level gormlogger.LogLevel
				leak  bool
			}{{gormlogger.Warn, false}, {gormlogger.Info, true}} {
				core, logs := observer.New(zapcore.DebugLevel)
				lg := database.NewGormLogger(zap.New(core), c.level, time.Nanosecond)
				var u model.User
				db.Session(&gorm.Session{Logger: lg}).Where("token = ?", secret).Take(&u)
				if logs.Len() == 0 {
					t.Fatal("慢查询应记录日志")
				}
				for _, e := range logs.All() {
					sql := e.ContextMap()["sql"].(string)
					if strings.Contains(sql, secret) != c.leak {
						t.Errorf("日志级别 %d：SQL 中参数值出现 = %v：%s", c.level, !c.leak, sql)
					}
				}
			}
		})
	}
}
