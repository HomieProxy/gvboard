package legacy

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/node"
)

// serverTypes 原项目节点类型（路由 server/{type}/...）。
var serverTypes = []string{"shadowsocks", "vmess", "vless", "trojan", "tuic", "hysteria", "anytls", "v2node"}

// ---------- 权限组 ----------

func (m *Module) adminGroupFetch(c *gin.Context) {
	in := params(c)
	if in.truthy("group_id") {
		var g *model.ServerGroup
		if id, valid := in.int("group_id"); valid {
			var row model.ServerGroup
			err := m.DB.WithContext(c.Request.Context()).Take(&row, id).Error
			switch {
			case err == nil:
				g = &row
			case !errors.Is(err, gorm.ErrRecordNotFound):
				m.fail(c, err)
				return
			}
		}
		ok(c, []*model.ServerGroup{g})
		return
	}
	list, err := m.Nodes.Groups(c.Request.Context())
	m.respond(c, list, err)
}

func (m *Module) adminGroupSave(c *gin.Context) {
	in := params(c)
	if !in.truthy("name") {
		m.fail(c, abortRaw("组名不能为空"))
		return
	}
	var id int64
	if in.truthy("id") {
		var valid bool
		if id, valid = in.id(); !valid {
			m.fail(c, abortRaw("组不存在"))
			return
		}
	}
	_, err := m.Nodes.SaveGroup(c.Request.Context(), id, in.str("name"))
	m.respond(c, true, err)
}

func (m *Module) adminGroupDrop(c *gin.Context) {
	id, _ := params(c).id()
	m.respond(c, true, m.Nodes.DeleteGroup(c.Request.Context(), id))
}

// ---------- 路由 ----------

func (m *Module) adminRouteFetch(c *gin.Context) {
	list, err := m.Nodes.Routes(c.Request.Context())
	m.respond(c, list, err)
}

var routeActions = []string{"block", "block_ip", "block_port", "protocol", "dns", "route", "route_ip", "default_out"}

func (m *Module) adminRouteSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("remarks", "备注不能为空")
	action := in.str("action")
	if action != "default_out" {
		v.required("match", "匹配值不能为空")
	}
	if v.present("match") {
		v.check("match", in.isArray("match"), "validation.array")
	}
	if v.required("action", "动作类型不能为空") {
		valid := false
		for _, a := range routeActions {
			valid = valid || action == a
		}
		v.check("action", valid, "动作类型参数有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	r := node.RouteInput{Remarks: in.str("remarks"), Match: in.strList("match"), Action: action}
	if in.raw("action_value") != nil {
		s := in.str("action_value")
		r.ActionValue = &s
	}
	var id int64
	if in.truthy("id") {
		var valid bool
		if id, valid = in.id(); !valid {
			m.fail(c, abortRaw("保存失败"))
			return
		}
	}
	err := m.Nodes.SaveRoute(c.Request.Context(), id, r)
	if id != 0 && isNotFound(err) {
		err = abortRaw("保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminRouteDrop(c *gin.Context) {
	id, _ := params(c).id()
	m.respond(c, true, m.Nodes.DeleteRoute(c.Request.Context(), id))
}

// ---------- 节点 ----------

func (m *Module) adminGetNodes(c *gin.Context) {
	list, err := m.Nodes.Overview(c.Request.Context())
	m.respond(c, list, err)
}

// adminServerSort 参数为 {类型: {节点ID: 排序值}}，与原项目一致。
func (m *Module) adminServerSort(c *gin.Context) {
	in := params(c)
	sorts := map[string]map[string]int64{}
	for _, typ := range serverTypes {
		items := map[string]int64{}
		switch x := in.raw(typ).(type) {
		case map[string]any:
			for id, v := range x {
				n, valid := input{"v": v}.int("v")
				if !valid {
					m.fail(c, abortRaw("保存失败"))
					return
				}
				items[id] = n
			}
		case []any:
			for i, v := range x {
				n, valid := input{"v": v}.int("v")
				if !valid {
					m.fail(c, abortRaw("保存失败"))
					return
				}
				items[strconv.Itoa(i)] = n
			}
		case nil:
			continue
		default:
			m.fail(c, abortRaw("保存失败"))
			return
		}
		sorts[typ] = items
	}
	m.respond(c, true, m.Nodes.Sort(c.Request.Context(), sorts))
}

// adminServerSave 新建或修改节点：请求参数转为 JSON 后交给原生服务，字段名与原项目一致。
func (m *Module) adminServerSave(typ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		in := params(c)
		var id int64
		if in.truthy("id") {
			var valid bool
			if id, valid = in.id(); !valid {
				m.fail(c, abortRaw("服务器不存在"))
				return
			}
		}
		body := make(map[string]any, len(in))
		for k, v := range in {
			if k != "id" && k != "auth_data" {
				body[k] = v
			}
		}
		raw, err := json.Marshal(body)
		if err != nil {
			m.fail(c, err)
			return
		}
		m.respond(c, true, m.Nodes.Save(c.Request.Context(), typ, id, raw))
	}
}

func (m *Module) adminServerDrop(typ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := params(c).id()
		m.respond(c, true, m.Nodes.Delete(c.Request.Context(), typ, id))
	}
}

// adminServerUpdate 只修改显示状态，没有 show 参数时不修改。
func (m *Module) adminServerUpdate(typ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		in := params(c)
		ctx := c.Request.Context()
		id, _ := in.id()
		if in.raw("show") == nil {
			srv, err := node.Find(ctx, m.DB, typ, id)
			if err == nil && srv == nil {
				err = abortRaw("该服务器不存在")
			}
			m.respond(c, true, err)
			return
		}
		s := in.str("show")
		if s != "0" && s != "1" {
			msg := "显示状态格式不正确"
			if typ == "vless" || typ == "v2node" {
				msg = "validation.in"
			}
			m.fail(c, &invalidError{fields: []fieldError{{field: "show", key: msg}}})
			return
		}
		m.respond(c, true, m.Nodes.SetShow(ctx, typ, id, int(s[0]-'0')))
	}
}

func (m *Module) adminServerCopy(typ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := params(c).id()
		m.respond(c, true, m.Nodes.Copy(c.Request.Context(), typ, id))
	}
}
