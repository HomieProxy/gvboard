package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"gvboard/internal/config"
)

func newInitCmd() *cobra.Command {
	var (
		opt   config.GenerateOptions
		force bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "生成配置文件（随机生成 API 前缀与签名密钥）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(cfgPath); err == nil && !force {
				return fmt.Errorf("配置文件 %s 已存在，如需覆盖请加 --force", cfgPath)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			content, err := config.Generate(opt)
			if err != nil {
				return err
			}
			if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
				return fmt.Errorf("写入配置文件失败: %w", err)
			}
			cmd.Printf("配置文件已生成：%s\n请填写数据库与 Redis 连接信息后执行 gvboard serve\n", cfgPath)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opt.AppKey, "app-key", "", "签名密钥，接入原生产库时填写原项目的 APP_KEY")
	f.StringVar(&opt.APIPrefix, "api-prefix", "", "原生 API 前缀，留空随机生成")
	f.StringVar(&opt.Mode, "mode", config.ModeAll, "部署模式：all（不分离）或 api（分离）")
	f.StringVar(&opt.DBDriver, "db", config.DriverMySQL, "数据库：mysql 或 postgres")
	f.BoolVar(&opt.Migrated, "migrated", false, "是否为从原项目迁移的站点（开启旧订阅路径兼容）")
	f.BoolVar(&force, "force", false, "覆盖已存在的配置文件")
	return cmd
}
