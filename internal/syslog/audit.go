package syslog

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/model"
)

// auditBodyMax 审计记录的请求体上限，超出部分不记录。
const auditBodyMax = 64 << 10

// capture 记录处理器读取的请求体（最多 auditBodyMax 字节）。
type capture struct {
	io.ReadCloser
	buf       bytes.Buffer
	truncated bool
}

func (c *capture) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		if room := auditBodyMax - c.buf.Len(); room > 0 {
			c.buf.Write(p[:min(n, room)])
			if n > room {
				c.truncated = true
			}
		} else {
			c.truncated = true
		}
	}
	return n, err
}

// Audit 管理端修改操作审计（对应原项目管理端 RequestLog 中间件，原项目只记录 POST 路径，请求参数原样写入）。
// 记录 POST / PUT / PATCH / DELETE：标题为"方法 路径"（管理端路径替换为 {admin}），请求参数脱敏后写入 data，
// 管理员与响应状态写入 context。必须放在管理员认证之后。
func (w *Writer) Audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			c.Next()
			return
		}
		var body *capture
		if c.Request.Body != nil {
			body = &capture{ReadCloser: c.Request.Body}
			c.Request.Body = body
		}
		c.Next()

		path := hideAdminPath(c.FullPath(), c.Request.URL.Path)
		data := map[string]any{}
		if q := c.Request.URL.Query(); len(q) > 0 {
			data["query"] = Mask(map[string][]string(q))
		}
		if body != nil && body.buf.Len() > 0 {
			data["body"] = parseBody(c.ContentType(), body.buf.Bytes())
			if body.truncated {
				data["body_truncated"] = true
			}
		}
		ctx := map[string]any{"status": c.Writer.Status()}
		if id := auth.Current(c); id != nil {
			ctx["admin_id"], ctx["admin_email"] = id.ID, id.Email
		}
		l := model.Log{
			Title: c.Request.Method + " " + path, Level: ptr("INFO"), Method: c.Request.Method, URI: path,
			IP: ptr(c.ClientIP()), Host: ptr(c.Request.Host), Context: jsonText(ctx),
		}
		if len(data) > 0 {
			l.Data = jsonText(data)
		}
		w.Add(l)
	}
}

// hideAdminPath 按路由模式中 :secure 段的位置，把实际路径中的管理端路径替换为 {admin}。
func hideAdminPath(pattern, path string) string {
	segs := strings.Split(path, "/")
	for i, s := range strings.Split(pattern, "/") {
		if s == ":"+auth.AdminParam && i < len(segs) {
			segs[i] = "{admin}"
		}
	}
	return strings.Join(segs, "/")
}

// parseBody 解析 JSON 或表单请求体并脱敏；无法解析时只记录长度（内容可能含敏感信息）。
func parseBody(contentType string, b []byte) any {
	if contentType == "application/x-www-form-urlencoded" {
		if v, err := url.ParseQuery(string(b)); err == nil {
			return Mask(map[string][]string(v))
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil {
		return Mask(v)
	}
	return map[string]any{"bytes": len(b)}
}
