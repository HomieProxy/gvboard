// Package web 不分离部署（server.mode=all）时托管前端页面（决策 D59、D60）：
//
//	GET /                     当前用户端主题的页面
//	GET /<文件>               当前主题的静态文件（theme.json、index.html 与隐藏文件除外）
//	GET /{安全路径}            跳转到 /{安全路径}/
//	GET /{安全路径}/           管理端页面
//	GET /{安全路径}/<文件>     管理端静态文件
//
// 前端使用带 # 的地址，服务器不做页面路由回退，其它地址仍为统一 404。
// 页面中注入 window.APP_CONFIG（API 前缀、站点公开配置、主题配置），前端无需再请求站点配置。
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/httpx"
	"gvboard/internal/passport"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/theme"
)

//go:embed all:admin
var adminFS embed.FS

// Module 前端页面托管。
type Module struct {
	Settings *setting.Service
	Themes   *theme.Manager

	api   string
	admin fs.FS
}

func (m *Module) Register(r *server.Router) {
	if r.Config.IsSeparated() {
		return
	}
	m.api = r.Config.API.Prefix
	if m.admin == nil {
		m.admin, _ = fs.Sub(adminFS, "admin")
	}
	r.AddFallback(m.fallback)
}

func (m *Module) fallback(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	p := c.Request.URL.Path
	if secure := m.Settings.String("secure_path"); secure != "" {
		base := "/" + secure
		if p == base {
			c.Header("Cache-Control", "no-store")
			c.Redirect(http.StatusFound, base+"/")
			return true
		}
		if rest, ok := strings.CutPrefix(p, base+"/"); ok {
			if rest == "" {
				m.adminIndex(c, secure)
				return true
			}
			return serveFile(c, m.admin, rest)
		}
	}
	if m.safeModeBlocked(c) {
		return false
	}
	if p == "/" {
		m.userIndex(c)
		return true
	}
	t, err := m.Themes.Active()
	if err != nil {
		return false
	}
	return serveFile(c, t.Files, strings.TrimPrefix(p, "/"))
}

// safeModeBlocked 开启安全模式后只允许通过站点地址的域名访问用户端（原项目返回 403，这里与其它不存在的地址一样返回 404）。
func (m *Module) safeModeBlocked(c *gin.Context) bool {
	if !m.Settings.Bool("safe_mode_enable") {
		return false
	}
	u, err := url.Parse(m.Settings.String("app_url"))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return !strings.EqualFold(strings.Trim(host, "[]"), u.Hostname())
}

func (m *Module) userIndex(c *gin.Context) {
	t, err := m.Themes.Active()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := t.Index()
	if err != nil {
		httpx.Fail(c, httpx.Internal(err))
		return
	}
	site := passport.BuildSiteConfig(m.Settings)
	writePage(c, page, site.Site.Name, site.Site.Description, gin.H{
		"api":      m.api,
		"site":     site.Site,
		"register": site.Register,
		"theme":    gin.H{"id": t.ID, "config": m.Themes.Config(t)},
	})
}

func (m *Module) adminIndex(c *gin.Context, secure string) {
	page, err := fs.ReadFile(m.admin, theme.IndexFile)
	if err != nil {
		httpx.Fail(c, httpx.Internal(err))
		return
	}
	s := m.Settings
	name := s.String("app_name")
	writePage(c, page, name, "", gin.H{
		"api":         m.api,
		"secure_path": secure,
		"site":        gin.H{"name": name, "logo": s.String("logo")},
		"theme": gin.H{
			"sidebar":        s.String("frontend_theme_sidebar"),
			"header":         s.String("frontend_theme_header"),
			"color":          s.String("frontend_theme_color"),
			"background_url": s.String("frontend_background_url"),
		},
	})
}

func writePage(c *gin.Context, page []byte, title, desc string, cfg any) {
	out, err := Inject(page, title, desc, cfg)
	if err != nil {
		httpx.Fail(c, httpx.Internal(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}

// Inject 替换页面标题，并在 </head> 前插入页面描述与 window.APP_CONFIG。
// 配置以 JSON 输出，其中的 < > & 已转义，不会提前结束 script 标签。
func Inject(page []byte, title, desc string, cfg any) ([]byte, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var head bytes.Buffer
	if desc != "" {
		head.WriteString(`<meta name="description" content="` + html.EscapeString(desc) + `">`)
	}
	head.WriteString("<script>window.APP_CONFIG=")
	head.Write(b)
	head.WriteString(";</script>")

	s := string(page)
	if title != "" {
		s = replaceTitle(s, html.EscapeString(title))
	}
	if i := indexFold(s, "</head>"); i >= 0 {
		return []byte(s[:i] + head.String() + s[i:]), nil
	}
	return []byte(head.String() + s), nil
}

func replaceTitle(s, title string) string {
	start := indexFold(s, "<title")
	if start < 0 {
		return s
	}
	gt := strings.IndexByte(s[start:], '>')
	if gt < 0 {
		return s
	}
	open := start + gt + 1
	end := indexFold(s[open:], "</title>")
	if end < 0 {
		return s
	}
	return s[:open] + title + s[open+end:]
}

func indexFold(s, sub string) int { return strings.Index(strings.ToLower(s), sub) }

// serveFile 输出静态文件；文件不存在、是目录、是主题定义 / 页面模板或隐藏文件时返回 false。
func serveFile(c *gin.Context, files fs.FS, name string) bool {
	if files == nil || name == "" || !fs.ValidPath(name) || name == theme.ManifestFile || name == theme.IndexFile {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}
	f, err := files.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = bytes.NewReader(b)
	}
	if strings.HasPrefix(name, "assets/") {
		// 构建产物文件名带内容哈希，可长期缓存。
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	if ct, ok := contentTypes[strings.ToLower(path.Ext(name))]; ok {
		c.Header("Content-Type", ct)
	}
	http.ServeContent(c.Writer, c.Request, path.Base(name), st.ModTime(), rs)
	return true
}

// contentTypes 常见前端文件类型。全局开启了 nosniff，类型错误会导致浏览器拒绝执行脚本，
// 不依赖系统注册表（Windows 上 .js 可能被注册为 text/plain）。
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".wasm":  "application/wasm",
	".txt":   "text/plain; charset=utf-8",
}
