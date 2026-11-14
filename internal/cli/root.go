// Package cli 定义 gvboard 命令行。
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// 版本信息，构建时通过 -ldflags "-X gvboard/internal/cli.Version=..." 注入。
var (
	Version   = "dev"
	BuildTime = ""
)

var cfgPath string

// NewRoot 创建根命令。
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "gvboard",
		Short:         "Gvboard 面板",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(os.Stdout)
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "config.yaml", "配置文件路径")
	root.AddCommand(newServeCmd(), newInitCmd(), newMigrateCmd(), newImportConfigCmd(), newDBConvertCmd(), newVersionCmd(),
		newJobCmd(), newCheckServerCmd(), newCreateAdminCmd(), newResetPasswordCmd(), newClearUserCmd(), newResetUserCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("gvboard %s %s\n", Version, BuildTime)
		},
	}
}
