package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
)

var (
	errGroupName       = httpx.Validation("组名不能为空")
	errGroupNotExist   = httpx.NotFoundErr("组不存在")
	errGroupUsedByNode = httpx.Conflict("该组已被节点所使用，无法删除")
	errGroupUsedByPlan = httpx.Conflict("该组已被订阅所使用，无法删除")
	errGroupUsedBySub  = httpx.Conflict("该组已被用户所使用，无法删除")
	errRouteNotExist   = httpx.NotFoundErr("路由不存在")
	errRouteRemarks    = httpx.Validation("备注不能为空")
	errRouteMatch      = httpx.Validation("匹配值不能为空")
	errRouteAction     = httpx.Validation("动作类型参数有误")
	errShowInvalid     = httpx.Validation("显示状态格式不正确")
	errShowNotExist    = httpx.NotFoundErr("该服务器不存在")
	errCopyFailed      = httpx.BadRequest("复制失败")
)

// Service 节点管理。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Log      *zap.Logger
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop()
}

func errField(err error) zap.Field { return zap.Error(err) }

// db 写入的 created_at / updated_at 使用服务的时钟。
func (s *Service) db(ctx context.Context) *gorm.DB {
	return s.DB.WithContext(ctx).Session(&gorm.Session{NowFunc: s.now})
}

// ---------- 权限组 ----------

// Group 权限组及使用情况。
type Group struct {
	model.ServerGroup
	UserCount   int64 `json:"user_count"`
	ServerCount int64 `json:"server_count"`
}

// Groups 权限组列表（对应原项目 server/group/fetch）。user_count 为使用该组的订阅数（决策 D33）。
func (s *Service) Groups(ctx context.Context) ([]Group, error) {
	db := s.db(ctx)
	var groups []model.ServerGroup
	if err := db.Order("id").Find(&groups).Error; err != nil {
		return nil, err
	}
	type row struct {
		GroupID int64
		N       int64
	}
	var rows []row
	if err := db.Model(&model.Subscription{}).Select("group_id, COUNT(*) AS n").
		Where("group_id IS NOT NULL").Group("group_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	subs := make(map[int64]int64, len(rows))
	for _, r := range rows {
		subs[r.GroupID] = r.N
	}
	servers, err := All(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(groups))
	for _, g := range groups {
		item := Group{ServerGroup: g, UserCount: subs[g.ID]}
		for _, srv := range servers {
			if srv.Common().GroupID.Contains(g.ID) {
				item.ServerCount++
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// SaveGroup 新建（id 为 0）或修改权限组名称。
func (s *Service) SaveGroup(ctx context.Context, id int64, name string) (*model.ServerGroup, error) {
	if name = strings.TrimSpace(name); name == "" {
		return nil, errGroupName
	}
	db := s.db(ctx)
	g := model.ServerGroup{Name: name}
	if id == 0 {
		return &g, db.Create(&g).Error
	}
	if err := db.Take(&g, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errGroupNotExist
		}
		return nil, err
	}
	g.Name = name
	return &g, db.Model(&g).Update("name", name).Error
}

// DeleteGroup 删除权限组。全部 8 类节点、套餐、订阅中有使用的不能删除（决策 D32）。
func (s *Service) DeleteGroup(ctx context.Context, id int64) error {
	db := s.db(ctx)
	var g model.ServerGroup
	if err := db.Take(&g, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errGroupNotExist
		}
		return err
	}
	servers, err := All(ctx, s.DB)
	if err != nil {
		return err
	}
	for _, srv := range servers {
		if srv.Common().GroupID.Contains(id) {
			return errGroupUsedByNode
		}
	}
	var n int64
	if err := db.Model(&model.Plan{}).Where("group_id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return errGroupUsedByPlan
	}
	if err := db.Model(&model.Subscription{}).Where("group_id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return errGroupUsedBySub
	}
	return db.Delete(&g).Error
}

// ---------- 路由规则 ----------

// Route 路由规则，match 为数组时解析后返回。
type Route struct {
	ID          int64   `json:"id"`
	Remarks     string  `json:"remarks"`
	Match       any     `json:"match"`
	Action      string  `json:"action"`
	ActionValue *string `json:"action_value"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

// Routes 路由规则列表（对应原项目 server/route/fetch）。
func (s *Service) Routes(ctx context.Context) ([]Route, error) {
	var rows []model.ServerRoute
	if err := s.db(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Route, 0, len(rows))
	for _, r := range rows {
		var match any = r.Match
		var arr any
		if json.Unmarshal([]byte(r.Match), &arr) == nil {
			switch arr.(type) {
			case []any, map[string]any:
				match = arr
			}
		}
		out = append(out, Route{ID: r.ID, Remarks: r.Remarks, Match: match, Action: r.Action,
			ActionValue: r.ActionValue, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

// RouteInput 路由规则参数。
type RouteInput struct {
	Remarks     string   `json:"remarks"`
	Match       []string `json:"match"`
	Action      string   `json:"action"`
	ActionValue *string  `json:"action_value"`
}

var routeActions = []string{"block", "block_ip", "block_port", "protocol", "dns", "route", "route_ip", "default_out"}

// SaveRoute 新建（id 为 0）或修改路由规则（对应原项目 server/route/save）。
// 匹配值去掉空项后保存为 JSON 数组，动作为 default_out 时匹配值为空数组。
func (s *Service) SaveRoute(ctx context.Context, id int64, in RouteInput) error {
	in.Remarks = strings.TrimSpace(in.Remarks)
	in.Action = strings.TrimSpace(in.Action)
	if in.Remarks == "" {
		return errRouteRemarks
	}
	valid := false
	for _, a := range routeActions {
		valid = valid || in.Action == a
	}
	if !valid {
		return errRouteAction
	}
	match := []string{}
	if in.Action != "default_out" {
		if len(in.Match) == 0 {
			return errRouteMatch
		}
		for _, m := range in.Match {
			if m = strings.TrimSpace(m); m != "" && m != "0" {
				match = append(match, m)
			}
		}
	}
	if in.ActionValue != nil {
		if v := strings.TrimSpace(*in.ActionValue); v == "" {
			in.ActionValue = nil
		} else {
			in.ActionValue = &v
		}
	}
	b, _ := json.Marshal(match)
	r := model.ServerRoute{Remarks: in.Remarks, Match: string(b), Action: in.Action, ActionValue: in.ActionValue}
	db := s.db(ctx)
	if id == 0 {
		return db.Create(&r).Error
	}
	var n int64
	if err := db.Model(&model.ServerRoute{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errRouteNotExist
	}
	r.ID = id
	return db.Model(&r).Select("remarks", "match", "action", "action_value", "updated_at").Updates(&r).Error
}

// DeleteRoute 删除路由规则。
func (s *Service) DeleteRoute(ctx context.Context, id int64) error {
	res := s.db(ctx).Delete(&model.ServerRoute{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errRouteNotExist
	}
	return nil
}

// ---------- 节点 ----------

// SetShow 显示或隐藏节点（对应原项目各类节点的 update）。
func (s *Service) SetShow(ctx context.Context, typ string, id int64, show int) error {
	if New(typ) == nil {
		return errTypeInvalid
	}
	if show != 0 && show != 1 {
		return errShowInvalid
	}
	srv, err := Find(ctx, s.DB, typ, id)
	if err != nil {
		return err
	}
	if srv == nil {
		return errShowNotExist
	}
	return s.db(ctx).Model(srv).Update("show", show).Error
}

// Copy 复制节点：新节点隐藏，其余字段（含创建时间）与原节点相同（与原项目一致）。
func (s *Service) Copy(ctx context.Context, typ string, id int64) error {
	if New(typ) == nil {
		return errTypeInvalid
	}
	srv, err := Find(ctx, s.DB, typ, id)
	if err != nil {
		return err
	}
	if srv == nil {
		return errServerNotExist
	}
	c := srv.Common()
	c.ID, c.Show = 0, 0
	if err := s.db(ctx).Create(srv).Error; err != nil {
		s.log().Warn("复制节点失败", errField(err))
		return errCopyFailed
	}
	return nil
}

// Delete 删除节点。
func (s *Service) Delete(ctx context.Context, typ string, id int64) error {
	if New(typ) == nil {
		return errTypeInvalid
	}
	srv, err := Find(ctx, s.DB, typ, id)
	if err != nil {
		return err
	}
	if srv == nil {
		return errNodeNotExist
	}
	return s.db(ctx).Delete(srv).Error
}

// Sort 批量设置排序，参数为 {类型: {节点ID: 排序值}}。任一节点不存在时全部不生效。
func (s *Service) Sort(ctx context.Context, in map[string]map[string]int64) error {
	return s.db(ctx).Transaction(func(tx *gorm.DB) error {
		for typ, items := range in {
			srv := New(typ)
			if srv == nil {
				return errTypeInvalid
			}
			for idStr, v := range items {
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil || id <= 0 {
					return errInvalid
				}
				var n int64
				if err := tx.Table(srv.TableName()).Where("id = ?", id).Count(&n).Error; err != nil {
					return err
				}
				if n == 0 {
					return errSaveFailed
				}
				if err := tx.Table(srv.TableName()).Where("id = ?", id).
					Updates(map[string]any{"sort": v, "updated_at": s.now().Unix()}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Status 节点状态（由节点通信接口写入缓存，子节点使用父节点的状态）。
type Status struct {
	Online          *int64 `json:"online"`
	LastCheckAt     *int64 `json:"last_check_at"`
	LastPushAt      *int64 `json:"last_push_at"`
	AvailableStatus int    `json:"available_status"` // 0 离线，1 在线但无推送，2 正常
}

// StatusOf 批量读取节点状态，返回与 servers 一一对应的结果。
func (s *Service) StatusOf(ctx context.Context, servers []Typed) ([]Status, error) {
	out := make([]Status, len(servers))
	if len(servers) == 0 || s.Cache == nil {
		return out, nil
	}
	names := []cache.KeyName{cache.KeyServerOnlineUser, cache.KeyServerLastCheckAt, cache.KeyServerLastPushAt}
	keys := make([]string, 0, len(servers)*len(names))
	for _, srv := range servers {
		id := srv.Common().ID
		if p := srv.Common().ParentID; p != nil {
			id = *p
		}
		for _, n := range names {
			keys = append(keys, s.Cache.KeyOf(n, cache.ServerKey(srv.Type, id)))
		}
	}
	vals, err := s.Cache.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	num := func(v any) *int64 {
		str, ok := v.(string)
		if !ok {
			return nil
		}
		var f float64
		if json.Unmarshal([]byte(str), &f) != nil {
			return nil
		}
		n := int64(f)
		return &n
	}
	deadline := s.now().Unix() - 300
	for i := range servers {
		st := Status{Online: num(vals[i*3]), LastCheckAt: num(vals[i*3+1]), LastPushAt: num(vals[i*3+2])}
		switch {
		case st.LastCheckAt == nil || deadline >= *st.LastCheckAt:
			st.AvailableStatus = 0
		case st.LastPushAt == nil || deadline >= *st.LastPushAt:
			st.AvailableStatus = 1
		default:
			st.AvailableStatus = 2
		}
		out[i] = st
	}
	return out, nil
}

// Overview 节点总览（对应原项目 server/manage/getNodes）：全部节点附加类型与状态，按 sort 升序（未设置的在前）。
// AnyTLS / v2node 的 padding_scheme 以 JSON 文本返回（与保存时的格式一致），v2node 附带一键安装命令。
func (s *Service) Overview(ctx context.Context) ([]map[string]any, error) {
	servers, err := All(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(servers, func(i, j int) bool {
		a, b := servers[i].Common().Sort, servers[j].Common().Sort
		switch {
		case a == nil:
			return b != nil
		case b == nil:
			return false
		}
		return *a < *b
	})
	status, err := s.StatusOf(ctx, servers)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(servers))
	for i, srv := range servers {
		b, err := json.Marshal(srv.Server)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["type"] = srv.Type
		m["online"], m["last_check_at"], m["last_push_at"] = status[i].Online, status[i].LastCheckAt, status[i].LastPushAt
		m["available_status"] = status[i].AvailableStatus
		if ps, ok := m["padding_scheme"]; ok && ps != nil {
			pb, _ := json.Marshal(ps)
			m["padding_scheme"] = string(pb)
		}
		if srv.Type == model.ServerTypeV2node {
			m["install_command"] = s.installCommand(srv.Common().ID)
		}
		out = append(out, m)
	}
	return out, nil
}

// installCommand v2node 一键安装命令（与原项目一致）。
func (s *Service) installCommand(id int64) string {
	host := s.Settings.String("server_api_url")
	if host == "" {
		host = s.Settings.String("app_url")
	}
	return fmt.Sprintf("wget -N https://raw.githubusercontent.com/wyx2685/v2node/master/script/install.sh && bash install.sh --api-host %s --node-id %d --api-key %s",
		shellQuote(host), id, shellQuote(s.Settings.String("server_token")))
}

// shellQuote 对应 PHP escapeshellarg（Linux）。
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
