// Package plugins 编译进程序的插件。新增插件：在 plugins/ 下建目录实现 pkg/plugin.Plugin，
// 在 init 中调用 plugin.Register，并在此处匿名导入。插件默认停用，需在管理端启用。
package plugins

import (
	_ "gvboard/plugins/checkin"
	_ "gvboard/plugins/firstorder"
	_ "gvboard/plugins/luckywheel"
	_ "gvboard/plugins/webhook"
)
