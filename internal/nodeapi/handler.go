package nodeapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vmihailenco/msgpack/v5"

	"gvboard/internal/audit"
	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/server"
)

// maxBody 节点上报请求体上限。
const maxBody = 64 << 20

// Module 节点通信路由与用户流量明细。
type Module struct {
	Svc  *Service
	Auth *auth.Service
	// Allow 节点接口来源 IP 白名单，为空不限制。
	Allow []netip.Prefix
	// Audit 审计规则服务，为 nil 时审计接口返回 404。
	Audit *audit.Service
}

// ParseWhitelist 解析配置 node.ip_whitelist（IP 或 CIDR）。
func ParseWhitelist(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("node.ip_whitelist 中的 %q 不是合法的 CIDR", s)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("node.ip_whitelist 中的 %q 不是合法的 IP", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Register 挂载路由。节点接口路径被节点后端写死，与原项目一致：
//
//	ANY /api/v1/server/{UniProxy|Deepbwork|TrojanTidalab|ShadowsocksTidalab}/{action}
//	ANY /api/v2/server/config
//
// 来源 IP 不在白名单、缺少或错误的 token、节点不存在时统一返回 404。
// 用户端：GET {api}/user/traffic-log  本月流量明细，参数 subscription_id 可选。
func (m *Module) Register(r *server.Router) {
	r.NodeV1.Group("", m.guard).Any("/:class/:action", m.dispatch)
	r.NodeV2.Group("", m.guard).Any("/config", m.v2Config)
	r.API.Group("/user", m.Auth.RequireUser()).GET("/traffic-log", m.trafficLog)
}

func (m *Module) guard(c *gin.Context) {
	if len(m.Allow) > 0 {
		ip, err := netip.ParseAddr(c.ClientIP())
		allowed := false
		if err == nil {
			ip = ip.Unmap()
			for _, p := range m.Allow {
				if p.Contains(ip) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			httpx.NotFound(c)
			return
		}
	}
	if !m.Svc.TokenOK(input(c, "token")) {
		httpx.NotFound(c)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	c.Next()
}

// input 读取参数，查询串优先，其次表单（与原项目 $request->input 一致）。
func input(c *gin.Context, k string) string {
	if v, ok := c.GetQuery(k); ok {
		return v
	}
	return c.PostForm(k)
}

func (m *Module) dispatch(c *gin.Context) {
	class := c.Param("class")
	if class != "" {
		class = strings.ToUpper(class[:1]) + class[1:]
	}
	action := strings.ToLower(c.Param("action"))
	switch class {
	case "UniProxy":
		m.uniProxy(c, action)
	case "Deepbwork":
		m.deepbwork(c, action)
	case "TrojanTidalab":
		m.trojanTidalab(c, action)
	case "ShadowsocksTidalab":
		m.ssTidalab(c, action)
	default:
		httpx.NotFound(c)
	}
}

func fail(c *gin.Context, err error) { httpx.Fail(c, httpx.Internal(err)) }

func sendJSON(c *gin.Context, v any) {
	b, err := encode(v)
	if err != nil {
		fail(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", b)
}

// sendTagged 带 ETag 的响应。tagOf 为计算 ETag 的内容（为空时用响应体）；
// exact 为 true 时 If-None-Match 必须与 ETag 完全相同才返回 304（v2 config），否则包含即可。
func sendTagged(c *gin.Context, body any, tagOf any, exact bool) {
	b, err := encode(body)
	if err != nil {
		fail(c, err)
		return
	}
	tb := b
	if tagOf != nil {
		if tb, err = encode(tagOf); err != nil {
			fail(c, err)
			return
		}
	}
	tag := etag(tb)
	inm := c.GetHeader("If-None-Match")
	if exact && inm == tag {
		c.Header("ETag", `"`+tag+`"`)
		c.Status(http.StatusNotModified)
		return
	}
	if !exact && inm != "" && strings.Contains(inm, tag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", `"`+tag+`"`)
	c.Data(http.StatusOK, "application/json", b)
}

func sendText(c *gin.Context, v any) {
	b, err := encode(v)
	if err != nil {
		fail(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=UTF-8", b)
}

func badData(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func body(c *gin.Context) ([]byte, bool) {
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return b, true
}

func (m *Module) uniProxy(c *gin.Context, action string) {
	ctx := c.Request.Context()
	typ := NodeType(input(c, "node_type"))
	srv, err := m.Svc.FindNode(ctx, typ, input(c, "node_id"))
	if err != nil {
		fail(c, err)
		return
	}
	if srv == nil {
		httpx.NotFound(c)
		return
	}
	id := srv.Common().ID
	switch action {
	case "user":
		if err := m.Svc.MarkCheck(ctx, typ, id); err != nil {
			fail(c, err)
			return
		}
		users, err := m.Svc.Users(ctx, srv.Common().GroupID)
		if err != nil {
			fail(c, err)
			return
		}
		if strings.Contains(c.GetHeader("X-Response-Format"), "msgpack") {
			sendMsgpack(c, users)
			return
		}
		sendTagged(c, obj{{"users", users}}, nil, false)
	case "push":
		b, ok := body(c)
		if !ok {
			return
		}
		data, count, err := ParseUniTraffic(b)
		if err != nil {
			badData(c, err)
			return
		}
		if err := m.Svc.MarkPush(ctx, typ, id, count); err != nil {
			fail(c, err)
			return
		}
		if err := m.Svc.Record(ctx, typ, srv, data); err != nil {
			fail(c, err)
			return
		}
		sendJSON(c, obj{{"data", true}})
	case "alivelist":
		alive, err := m.Svc.AliveList(ctx)
		if err != nil {
			fail(c, err)
			return
		}
		sendJSON(c, obj{{"alive", alive}})
	case "alive":
		b, ok := body(c)
		if !ok {
			return
		}
		data, err := ParseAlive(b)
		if err != nil {
			badData(c, err)
			return
		}
		if err := m.Svc.Alive(ctx, typ, id, data); err != nil {
			fail(c, err)
			return
		}
		sendJSON(c, obj{{"data", true}})
	case "config":
		o, err := m.Svc.UniConfig(ctx, srv)
		if err != nil {
			fail(c, err)
			return
		}
		sendTagged(c, o, nil, false)
	case "audit":
		m.audit(c, typ, srv)
	default:
		httpx.NotFound(c)
	}
}

// audit 审计规则：GET 拉取节点生效的规则 {"rules":[{"id","action","match"}]}（带 ETag），
// POST 上报命中记录 {"records":[...]}。未启用审计模块时返回 404。
func (m *Module) audit(c *gin.Context, typ string, srv node.Server) {
	if m.Audit == nil {
		httpx.NotFound(c)
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodGet {
		rules, err := m.Audit.NodeRules(ctx, srv)
		if err != nil {
			fail(c, err)
			return
		}
		sendTagged(c, obj{{"rules", rules}}, nil, false)
		return
	}
	b, ok := body(c)
	if !ok {
		return
	}
	var in struct {
		Records []audit.Record `json:"records"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		badData(c, fmt.Errorf("invalid audit data: %w", err))
		return
	}
	if err := m.Audit.Report(ctx, typ, srv, in.Records); err != nil {
		fail(c, err)
		return
	}
	sendJSON(c, obj{{"data", true}})
}

func sendMsgpack(c *gin.Context, users []User) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	enc.UseCompactInts(true)
	if err := enc.Encode(map[string]any{"users": users}); err != nil {
		fail(c, err)
		return
	}
	tag := etag(buf.Bytes())
	if inm := c.GetHeader("If-None-Match"); inm != "" && strings.Contains(inm, tag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", `"`+tag+`"`)
	c.Data(http.StatusOK, "application/x-msgpack", buf.Bytes())
}

func (m *Module) v2Config(c *gin.Context) {
	ctx := c.Request.Context()
	srv, err := m.Svc.FindNode(ctx, model.ServerTypeV2node, input(c, "node_id"))
	if err != nil {
		fail(c, err)
		return
	}
	if srv == nil {
		httpx.NotFound(c)
		return
	}
	o, err := m.Svc.V2Config(ctx, srv.(*model.ServerV2node))
	if err != nil {
		fail(c, err)
		return
	}
	sendTagged(c, o, nil, true)
}

// legacyNode 旧版接口按固定类型读取节点，不存在时返回 404 并返回 nil。
func (m *Module) legacyNode(c *gin.Context, typ string) node.Server {
	s, err := m.Svc.FindNode(c.Request.Context(), typ, input(c, "node_id"))
	if err != nil {
		fail(c, err)
		return nil
	}
	if s == nil {
		httpx.NotFound(c)
	}
	return s
}

// legacyUsersReply 旧版接口的用户列表：记录检查时间后返回 {"msg":"ok","data":[...]}。
func (m *Module) legacyUsersReply(c *gin.Context, typ string, id int64, groups model.IntList, build func([]User) []obj, wrap func([]obj) obj) {
	ctx := c.Request.Context()
	if err := m.Svc.MarkCheck(ctx, typ, id); err != nil {
		fail(c, err)
		return
	}
	users, err := m.Svc.Users(ctx, groups)
	if err != nil {
		fail(c, err)
		return
	}
	result := build(users)
	sendTagged(c, wrap(result), result, false)
}

func msgOK(result []obj) obj    { return obj{{"msg", "ok"}, {"data", result}} }
func dataOnly(result []obj) obj { return obj{{"data", result}} }

// legacySubmit 旧版接口的流量上报 [{"user_id","u","d"}]，返回 {"ret":1,"msg":"ok"}。
func (m *Module) legacySubmit(c *gin.Context, typ string, srv node.Server) {
	ctx := c.Request.Context()
	b, ok := body(c)
	if !ok {
		return
	}
	data, count, err := ParseLegacyTraffic(b)
	if err != nil {
		badData(c, err)
		return
	}
	if err := m.Svc.MarkPush(ctx, typ, srv.Common().ID, count); err != nil {
		fail(c, err)
		return
	}
	if err := m.Svc.Record(ctx, typ, srv, data); err != nil {
		fail(c, err)
		return
	}
	sendJSON(c, obj{{"ret", 1}, {"msg", "ok"}})
}

// localPort 旧版配置接口的 local_port 参数，缺少或非法时返回 false。
func localPort(c *gin.Context) (int64, bool) {
	v := input(c, "local_port")
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n == 0 || input(c, "node_id") == "" {
		return 0, false
	}
	return n, true
}

func (m *Module) deepbwork(c *gin.Context, action string) {
	switch action {
	case "user", "submit", "config":
	default:
		httpx.NotFound(c)
		return
	}
	port, portOK := localPort(c)
	if action == "config" && !portOK {
		httpx.NotFound(c)
		return
	}
	s := m.legacyNode(c, model.ServerTypeVmess)
	if s == nil {
		return
	}
	srv := s.(*model.ServerVmess)
	switch action {
	case "user":
		m.legacyUsersReply(c, model.ServerTypeVmess, srv.ID, srv.GroupID,
			func(u []User) []obj { return legacyUsers(u, "v2ray_user", v2rayUser) }, msgOK)
	case "submit":
		m.legacySubmit(c, model.ServerTypeVmess, srv)
	case "config":
		sendText(c, v2rayConfig(srv, port, m.Svc.Settings.Bool("server_log_enable")))
	}
}

func (m *Module) trojanTidalab(c *gin.Context, action string) {
	switch action {
	case "user", "submit", "config":
	default:
		httpx.NotFound(c)
		return
	}
	port, portOK := localPort(c)
	if action == "config" && !portOK {
		httpx.NotFound(c)
		return
	}
	s := m.legacyNode(c, model.ServerTypeTrojan)
	if s == nil {
		return
	}
	srv := s.(*model.ServerTrojan)
	switch action {
	case "user":
		m.legacyUsersReply(c, model.ServerTypeTrojan, srv.ID, srv.GroupID,
			func(u []User) []obj { return legacyUsers(u, "trojan_user", trojanUser) }, msgOK)
	case "submit":
		m.legacySubmit(c, model.ServerTypeTrojan, srv)
	case "config":
		sendText(c, trojanConfig(srv, port))
	}
}

func (m *Module) ssTidalab(c *gin.Context, action string) {
	if action != "user" && action != "submit" {
		httpx.NotFound(c)
		return
	}
	s := m.legacyNode(c, model.ServerTypeShadowsocks)
	if s == nil {
		return
	}
	srv := s.(*model.ServerShadowsocks)
	if action == "submit" {
		m.legacySubmit(c, model.ServerTypeShadowsocks, srv)
		return
	}
	m.legacyUsersReply(c, model.ServerTypeShadowsocks, srv.ID, srv.GroupID,
		func(u []User) []obj { return ssTidalabUsers(u, srv) }, dataOnly)
}

func (m *Module) trafficLog(c *gin.Context) {
	var subID *int64
	if v := c.Query("subscription_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, httpx.Validation("Invalid parameters"))
			return
		}
		subID = &n
	}
	rows, err := m.Svc.TrafficLogs(c.Request.Context(), auth.Current(c).ID, subID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, rows)
}
