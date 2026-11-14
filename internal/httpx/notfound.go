package httpx

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// 内置 404 页面，与 nginx 默认页面一致，部署在 nginx 之后时无法与普通站点区分。
const defaultNotFoundPage = "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

var notFoundPage atomic.Value

func init() { notFoundPage.Store([]byte(defaultNotFoundPage)) }

// SetNotFoundPage 替换 404 页面内容（启动时调用）。
func SetNotFoundPage(b []byte) {
	if len(b) > 0 {
		notFoundPage.Store(b)
	}
}

// NotFound 统一 404 响应。
// 所有"路径不存在"、"兼容层关闭"、"节点/订阅 token 无效"等需要隐藏的场景都必须调用此函数，
// 保证状态码、响应头、响应体与访问任意不存在的路径完全一致。
func NotFound(c *gin.Context) {
	c.Header("Content-Type", "text/html")
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNotFound)
	_, _ = c.Writer.Write(notFoundPage.Load().([]byte))
	c.Abort()
}
