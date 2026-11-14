package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gvboard/internal/app"
	"gvboard/internal/migrate"
	"gvboard/internal/setting"
)

func newMigrateCmd() *cobra.Command {
	var status bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "创建或升级数据库结构，并把原项目数据迁移为多订阅结构（可重复执行）",
		Long: `创建或升级数据库结构：
  - 表不存在时创建（全新安装）；表已存在时只补充缺失的列与 Gvboard 所需索引，不修改原有列。
  - 一次性迁移步骤（如把用户订阅复制到 v2_subscription）执行后会记录，重复执行不会重复迁移。
接入原生产库前请务必先备份数据库，并停止原面板运行。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.New(cfgPath, app.WithoutCache(), app.SkipSettingsLoad())
			if err != nil {
				return err
			}
			defer a.Close()
			ctx := context.Background()
			m := migrate.New(a.DB, a.Config.Database.Driver)

			if status {
				pending, err := m.Pending(ctx)
				if err != nil {
					return err
				}
				if len(pending) == 0 {
					cmd.Println("没有待执行的迁移步骤")
					return nil
				}
				cmd.Println("待执行的迁移步骤：")
				for _, s := range pending {
					cmd.Printf("  %s  %s\n", s.ID, s.Description)
				}
				return nil
			}

			rep, err := m.Run(ctx)
			printMigrateReport(cmd, rep)
			if err != nil {
				return err
			}
			if err := a.Settings.Load(ctx); err != nil {
				return err
			}
			notices, err := a.Settings.EnsureDefaults(ctx)
			for _, n := range notices {
				cmd.Println("  " + n)
			}
			if err != nil {
				return err
			}
			cmd.Println("数据库迁移完成")
			return nil
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "只显示待执行的迁移步骤，不做任何修改")
	return cmd
}

func printMigrateReport(cmd *cobra.Command, rep *migrate.Report) {
	if rep == nil {
		return
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		cmd.Printf("%s（%d）：\n  %s\n", title, len(items), strings.Join(items, "\n  "))
	}
	section("新建表", rep.CreatedTables)
	section("新增列", rep.AddedColumns)
	section("新建索引", rep.CreatedIndexes)
	section("执行迁移步骤", rep.AppliedSteps)
	section("说明", rep.Notices)
}

func newImportConfigCmd() *cobra.Command {
	var (
		file   string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "import-config",
		Short: "导入原项目的系统设置文件 config/v2board.php",
		RunE: func(cmd *cobra.Command, _ []string) error {
			src, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("读取文件失败: %w", err)
			}
			if dryRun {
				raw, err := setting.ParsePHPConfig(src)
				if err != nil {
					return err
				}
				_, rep := setting.PrepareImport(raw)
				cmd.Println("预览模式，不写入数据库")
				printImportReport(cmd, rep)
				return nil
			}

			a, err := app.New(cfgPath, app.WithoutCache())
			if err != nil {
				return err
			}
			defer a.Close()
			rep, err := a.Settings.ImportPHP(context.Background(), src)
			printImportReport(cmd, rep)
			if err != nil {
				return err
			}
			cmd.Println("配置导入完成")
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "原项目 config/v2board.php 文件路径")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只解析并显示结果，不写入数据库")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func printImportReport(cmd *cobra.Command, rep *setting.ImportReport) {
	if rep == nil {
		return
	}
	cmd.Printf("已导入设置项：%d 个\n", len(rep.Imported))
	if len(rep.Unknown) > 0 {
		cmd.Printf("未定义但已原样保存：%s\n", strings.Join(rep.Unknown, ", "))
	}
	for k, reason := range rep.Skipped {
		cmd.Printf("已跳过 %s：%s\n", k, reason)
	}
	for _, n := range rep.Notices {
		cmd.Println("说明：" + n)
	}
}
