package subscribe

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// TemplateInfo 订阅模板（管理端编辑用）。
type TemplateInfo struct {
	Name       string `json:"name"`
	SettingKey string `json:"setting_key"`
	Format     string `json:"format"` // yaml、json、text
	Custom     bool   `json:"custom"` // 是否已设置自定义模板
	Default    string `json:"default"`
}

var tplOrder = []string{tplClash, tplStash, tplAppClash, tplSingbox, tplSingboxOld, tplSurge, tplSurfboard}

// Templates 全部订阅模板及其内置默认内容（决策 D44）。
func (s *Service) Templates() ([]TemplateInfo, error) {
	out := make([]TemplateInfo, 0, len(tplOrder))
	for _, name := range tplOrder {
		def := tplDefs[name]
		b, err := rulesFS.ReadFile("rules/" + def.file)
		if err != nil {
			return nil, err
		}
		format := map[tplKind]string{kindText: "text", kindYAML: "yaml", kindJSON: "json"}[def.kind]
		out = append(out, TemplateInfo{
			Name: name, SettingKey: SettingKey(name), Format: format,
			Custom: s.Settings.String(SettingKey(name)) != "", Default: string(b),
		})
	}
	return out, nil
}

// TestInput 规则测试参数：规则与示例请求。Client 为空时按 flag（或 User-Agent）识别。
type TestInput struct {
	Rule      RuleInput         `json:"rule"`
	IP        string            `json:"ip"`
	UserAgent string            `json:"user_agent"`
	Flag      string            `json:"flag"`
	Headers   map[string]string `json:"headers"`
	Client    string            `json:"client"`
}

func (in TestInput) access() Access {
	headers := make(map[string]string, len(in.Headers))
	for k, v := range in.Headers {
		headers[http.CanonicalHeaderKey(strings.TrimSpace(k))] = v
	}
	return Access{
		IP: strings.TrimSpace(in.IP), UserAgent: in.UserAgent, Flag: in.Flag,
		Header: func(name string) string { return headers[http.CanonicalHeaderKey(name)] },
	}
}

var errAdminInvalid = httpx.Validation("参数有误")

// registerAdmin 挂载管理端接口（决策 D46）：
//
//	GET    subscribe-guard/rules             规则列表（按匹配顺序）
//	POST   subscribe-guard/rules             新建规则
//	PUT    subscribe-guard/rules/:id         修改规则
//	DELETE subscribe-guard/rules/:id         删除规则
//	POST   subscribe-guard/rules/test        用示例请求测试规则
//	GET    subscribe-guard/clients           可用于规则的客户端格式名称
//	GET    subscribe-guard/logs              请求日志（筛选、分页）
//	POST   subscribe-guard/logs/clean        立即清理超过保留天数的日志
//	GET    subscribe-guard/stats             按订阅汇总（hours、order=ips|requests、limit）
//	GET    subscribe-guard/invalid           每天的无效 token 请求次数（days）
//	GET    subscribe-templates               订阅模板与内置默认内容
func (m *Module) registerAdmin(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/subscribe-guard/rules", m.adminRules)
	a.POST("/subscribe-guard/rules", m.adminSaveRule)
	a.PUT("/subscribe-guard/rules/:id", m.adminSaveRule)
	a.DELETE("/subscribe-guard/rules/:id", m.adminDeleteRule)
	a.POST("/subscribe-guard/rules/test", m.adminTestRule)
	a.GET("/subscribe-guard/clients", func(c *gin.Context) { httpx.OK(c, m.Svc.ClientNames()) })
	a.GET("/subscribe-guard/logs", m.adminLogs)
	a.POST("/subscribe-guard/logs/clean", m.adminCleanLogs)
	a.GET("/subscribe-guard/stats", m.adminStats)
	a.GET("/subscribe-guard/invalid", m.adminInvalid)
	a.GET("/subscribe-templates", m.adminTemplates)
}

func adminRespond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func (m *Module) adminRules(c *gin.Context) {
	rules, err := m.Svc.Rules(c.Request.Context())
	adminRespond(c, rules, err)
}

func (m *Module) adminSaveRule(c *gin.Context) {
	var id int64
	if p := c.Param("id"); p != "" {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, errAdminInvalid)
			return
		}
		id = n
	}
	var in RuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	r, err := m.Svc.SaveRule(c.Request.Context(), id, in)
	adminRespond(c, r, err)
}

func (m *Module) adminDeleteRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	adminRespond(c, true, m.Svc.DeleteRule(c.Request.Context(), id))
}

func (m *Module) adminTestRule(c *gin.Context) {
	var in TestInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	hit, err := m.Svc.TestRule(in.Rule, in.access(), in.Client)
	adminRespond(c, gin.H{"matched": hit}, err)
}

func (m *Module) adminLogs(c *gin.Context) {
	var q LogQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	page, err := m.Svc.Logs(c.Request.Context(), q)
	adminRespond(c, page, err)
}

func (m *Module) adminCleanLogs(c *gin.Context) {
	n, err := m.Svc.CleanLogs(c.Request.Context())
	adminRespond(c, gin.H{"deleted": n}, err)
}

func queryInt(c *gin.Context, key string, def, lo, hi int64) (int64, bool) {
	v := c.Query(key)
	if v == "" {
		return def, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= lo && n <= hi
}

func (m *Module) adminStats(c *gin.Context) {
	hours, ok1 := queryInt(c, "hours", 24, 1, 24*180)
	limit, ok2 := queryInt(c, "limit", 50, 1, 200)
	order := c.DefaultQuery("order", "ips")
	if !ok1 || !ok2 || (order != "ips" && order != "requests") {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	stats, err := m.Svc.SubStats(c.Request.Context(), m.Svc.now().Unix()-hours*3600, order, int(limit))
	adminRespond(c, stats, err)
}

func (m *Module) adminInvalid(c *gin.Context) {
	days, ok := queryInt(c, "days", 30, 1, 180)
	if !ok {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	counts, err := m.Svc.InvalidCounts(c.Request.Context(), int(days))
	adminRespond(c, counts, err)
}

func (m *Module) adminTemplates(c *gin.Context) {
	list, err := m.Svc.Templates()
	adminRespond(c, list, err)
}
