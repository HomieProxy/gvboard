package main

import (
	"fmt"
	"os"
	// 内嵌时区数据：没有系统时区库的环境（Windows、精简系统）也能加载 app.timezone
	_ "time/tzdata"

	"gvboard/internal/cli"
	_ "gvboard/plugins"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
