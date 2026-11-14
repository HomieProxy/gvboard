package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gvboard/internal/app"
	"gvboard/internal/config"
	"gvboard/internal/database"
	"gvboard/internal/migrate"
)

var logTables = []string{"v2_log", "v2_mail_log", "v2_subscribe_log"}

func newDBConvertCmd() *cobra.Command {
	var (
		to       string
		skipLogs bool
		batch    int
	)
	cmd := &cobra.Command{
		Use:   "db-convert",
		Short: "把当前 MySQL 数据库全量复制到 PostgreSQL 空库",
		Long: `把配置文件中的 MySQL 数据库全量复制到 PostgreSQL：
  1. 源库必须已执行 gvboard migrate；目标库必须是新建的空库（上次转换失败留下的空表可以直接重试）。
  2. 自动在目标库创建表结构，在一个事务中复制全部数据、同步自增序列并逐表核对行数；失败时目标库不会留下数据。
  3. 源库在只读的一致性快照中读取，但仍建议先停止 Gvboard（serve）再转换，避免转换期间产生的新数据丢失。
完成后把配置文件的 database 改为 PostgreSQL，再执行 gvboard migrate 检查一次即可启动。`,
		Example: `  # 地址中不写密码时读取环境变量 PGPASSWORD（推荐，避免密码留在命令历史中）
  gvboard db-convert --to "postgres://gvboard@127.0.0.1:5432/gvboard?sslmode=disable"
  gvboard db-convert --to "postgres://..." --skip-logs`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.New(cfgPath, app.WithoutCache(), app.SkipSettingsLoad())
			if err != nil {
				return err
			}
			defer a.Close()
			if a.Config.Database.Driver != config.DriverMySQL {
				return fmt.Errorf("源数据库必须是 MySQL（当前配置为 %s）", a.Config.Database.Driver)
			}
			if !strings.HasPrefix(to, "postgres://") && !strings.HasPrefix(to, "postgresql://") {
				return fmt.Errorf("--to 必须是 postgres:// 开头的连接地址")
			}
			dst, err := gorm.Open(postgres.Open(to), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
			if err != nil {
				return fmt.Errorf("连接目标数据库失败: %w", err)
			}
			defer database.Close(dst)
			ctx := context.Background()
			if sqlDB, err := dst.DB(); err != nil {
				return err
			} else if err := sqlDB.PingContext(ctx); err != nil {
				return fmt.Errorf("目标数据库不可达: %w", err)
			}

			opt := migrate.ConvertOptions{BatchSize: batch, SkipTables: map[string]bool{}}
			if skipLogs {
				for _, t := range logTables {
					opt.SkipTables[t] = true
				}
			}
			start := time.Now()
			opt.Progress = func(table string, rows int64) {
				cmd.Printf("  %-26s %d 行\n", table, rows)
			}
			cmd.Println("开始复制数据：")
			rep, err := migrate.Convert(ctx, a.DB, dst, opt)
			if rep != nil {
				if len(rep.Skipped) > 0 {
					cmd.Printf("只创建表结构、未复制数据：%s\n", strings.Join(rep.Skipped, ", "))
				}
				if len(rep.IgnoredTables) > 0 {
					cmd.Printf("源库中不属于 Gvboard 的表（未复制）：%s\n", strings.Join(rep.IgnoredTables, ", "))
				}
				if len(rep.IgnoredColumns) > 0 {
					cmd.Printf("源库中多出的列（未复制）：%s\n", strings.Join(rep.IgnoredColumns, ", "))
				}
				for _, n := range rep.Notices {
					cmd.Println("说明：" + n)
				}
			}
			if err != nil {
				return err
			}
			var total int64
			for _, t := range rep.Tables {
				total += t.Rows
			}
			cmd.Printf("转换完成：%d 张表，%d 行，用时 %s\n", len(rep.Tables), total, time.Since(start).Round(time.Second))
			cmd.Println("下一步：把配置文件 database 改为 PostgreSQL（driver: postgres），执行 gvboard migrate 检查后启动")
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "目标 PostgreSQL 连接地址，如 postgres://用户:密码@主机:5432/库名?sslmode=disable")
	cmd.Flags().BoolVar(&skipLogs, "skip-logs", false, "不复制系统日志、邮件日志、订阅请求日志（只建表）")
	cmd.Flags().IntVar(&batch, "batch", 1000, "每批复制的行数")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
