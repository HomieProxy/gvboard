package audit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/setting"
)

const (
	// maxRecords 一次上报最多处理的记录数，超出部分丢弃。
	maxRecords = 5000
	cleanBatch = 5000
)

var (
	errInvalid      = httpx.Validation("参数有误")
	errRuleNotExist = httpx.NotFoundErr("规则不存在")
)

// BanInfo 审计封禁的信息，用于通知管理员。
type BanInfo struct {
	UserID   int64
	Email    string
	RuleName string
	Target   string
	Server   string
}

// Service 审计规则服务。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Log      *zap.Logger
	Now      func() time.Time
	// RemoveSessions 删除用户全部登录会话（封禁时调用），可为 nil。
	RemoveSessions func(ctx context.Context, userID int64) error
	// OnBan 审计封禁账号后调用（通知管理员），可为 nil。
	OnBan func(ctx context.Context, info BanInfo)
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

// ---------- 规则管理 ----------

// RuleInput 管理端提交的规则。
type RuleInput struct {
	Name    string  `json:"name"`
	Pattern string  `json:"pattern"`
	Action  string  `json:"action"`
	GroupID []int64 `json:"group_id"`
	Enabled bool    `json:"enabled"`
	Sort    int64   `json:"sort"`
}

func validAction(a string) bool {
	return a == model.AuditActionLog || a == model.AuditActionBlock || a == model.AuditActionBan
}

// Rules 全部规则，按排序值、ID 排列。
func (s *Service) Rules(ctx context.Context) ([]model.AuditRule, error) {
	out := []model.AuditRule{}
	err := s.DB.WithContext(ctx).Order("sort ASC, id ASC").Find(&out).Error
	return out, err
}

// SaveRule 新建（id 为 0）或修改规则。
func (s *Service) SaveRule(ctx context.Context, id int64, in RuleInput) (*model.AuditRule, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return nil, httpx.Validation("规则名称不能为空且不能超过 64 个字符")
	}
	if !validAction(in.Action) {
		return nil, httpx.Validation("动作不正确")
	}
	if in.Sort < -1000000 || in.Sort > 1000000 {
		return nil, httpx.Validation("排序取值范围为 -1000000～1000000")
	}
	if _, _, err := parsePattern(in.Pattern); err != nil {
		return nil, httpx.Validation("匹配内容有误：" + err.Error())
	}
	var groups model.IntList
	for _, g := range in.GroupID {
		if g <= 0 {
			return nil, errInvalid
		}
		if !groups.Contains(g) {
			groups = append(groups, g)
		}
	}
	if len(groups) > 0 {
		var n int64
		if err := s.DB.WithContext(ctx).Model(&model.ServerGroup{}).Where("id IN ?", []int64(groups)).Count(&n).Error; err != nil {
			return nil, err
		}
		if int(n) != len(groups) {
			return nil, httpx.Validation("权限组不存在")
		}
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	r := &model.AuditRule{}
	db := s.DB.WithContext(ctx)
	if id > 0 {
		err := db.Take(r, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errRuleNotExist
		}
		if err != nil {
			return nil, err
		}
	}
	r.Name, r.Pattern, r.Action, r.GroupID, r.Enabled, r.Sort = name, strings.TrimSpace(in.Pattern), in.Action, groups, enabled, in.Sort
	if id > 0 {
		err := db.Model(r).Select("name", "pattern", "action", "group_id", "enabled", "sort", "updated_at").Updates(r).Error
		return r, err
	}
	return r, db.Create(r).Error
}

// DeleteRule 删除规则，审计日志保留。
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Delete(&model.AuditRule{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errRuleNotExist
	}
	return nil
}

// TestHit 测试结果中命中的规则。
type TestHit struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	Enabled int    `json:"enabled"`
	Line    string `json:"line"`
}

// Test 用一个目标（example.com:443、1.2.3.4:443 等）测试全部规则，返回命中的规则与匹配到的行。
func (s *Service) Test(ctx context.Context, target string) ([]TestHit, error) {
	t, err := ParseTarget(target)
	if err != nil {
		return nil, httpx.Validation(err.Error())
	}
	rules, err := s.Rules(ctx)
	if err != nil {
		return nil, err
	}
	out := []TestHit{}
	for _, r := range rules {
		entries, lines, err := parsePattern(r.Pattern)
		if err != nil {
			continue
		}
		for i, e := range entries {
			if e.match(t) {
				out = append(out, TestHit{ID: r.ID, Name: r.Name, Action: r.Action, Enabled: r.Enabled, Line: lines[i]})
				break
			}
		}
	}
	return out, nil
}

// ---------- 节点接口 ----------

// NodeRule 下发给节点的规则。
type NodeRule struct {
	ID     int64    `json:"id"`
	Action string   `json:"action"`
	Match  []string `json:"match"`
}

// applies 规则是否对节点生效：未选权限组时全部节点生效，否则节点须属于其中一个权限组。
func applies(r *model.AuditRule, groups model.IntList) bool {
	if len(r.GroupID) == 0 {
		return true
	}
	for _, g := range r.GroupID {
		if groups.Contains(g) {
			return true
		}
	}
	return false
}

// NodeRules 节点生效的已启用规则（匹配内容为规范化后的行）。
func (s *Service) NodeRules(ctx context.Context, srv node.Server) ([]NodeRule, error) {
	var rules []model.AuditRule
	if err := s.DB.WithContext(ctx).Where("enabled = ?", 1).Order("sort ASC, id ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	out := []NodeRule{}
	for i := range rules {
		r := &rules[i]
		if !applies(r, srv.Common().GroupID) {
			continue
		}
		_, lines, err := parsePattern(r.Pattern)
		if err != nil {
			s.log().Warn("审计规则无效，已跳过", zap.Int64("rule_id", r.ID), zap.Error(err))
			continue
		}
		out = append(out, NodeRule{ID: r.ID, Action: r.Action, Match: lines})
	}
	return out, nil
}

// Record 节点上报的一条命中记录。UserID 为节点侧的用户 ID，即订阅 ID。
type Record struct {
	UserID  int64  `json:"user_id"`
	RuleID  int64  `json:"rule_id"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Network string `json:"network"`
	IP      string `json:"ip"`
	Count   int64  `json:"count"`
	Time    int64  `json:"time"`
}

func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Report 处理节点上报：写入审计日志、累计规则命中数；规则当前动作为封禁、且节点已按封禁处理时封禁账号。
// 不存在的订阅、不存在或对该节点不生效的规则，记录直接丢弃。
func (s *Service) Report(ctx context.Context, typ string, srv node.Server, records []Record) error {
	if len(records) > maxRecords {
		records = records[:maxRecords]
	}
	if len(records) == 0 {
		return nil
	}
	ruleIDs, subIDs := []int64{}, []int64{}
	for _, r := range records {
		ruleIDs, subIDs = append(ruleIDs, r.RuleID), append(subIDs, r.UserID)
	}
	db := s.DB.WithContext(ctx)
	var rules []model.AuditRule
	if err := db.Where("id IN ?", uniq(ruleIDs)).Find(&rules).Error; err != nil {
		return err
	}
	ruleByID := make(map[int64]*model.AuditRule, len(rules))
	for i := range rules {
		if applies(&rules[i], srv.Common().GroupID) {
			ruleByID[rules[i].ID] = &rules[i]
		}
	}
	var subs []model.Subscription
	if err := db.Select("id", "user_id").Where("id IN ?", uniq(subIDs)).Find(&subs).Error; err != nil {
		return err
	}
	owner := make(map[int64]int64, len(subs))
	for _, sub := range subs {
		owner[sub.ID] = sub.UserID
	}

	now := s.now().Unix()
	serverID := srv.Common().ID
	type hit struct {
		n    int64
		last int64
	}
	hits := map[int64]hit{}
	bans := map[int64]int{} // 账号 → 触发封禁的日志下标
	var logs []model.AuditLog
	for _, r := range records {
		rule, uid := ruleByID[r.RuleID], owner[r.UserID]
		if rule == nil || uid == 0 || !validAction(r.Action) {
			continue
		}
		count := max(r.Count, 1)
		at := r.Time
		if at <= 0 || at > now+300 {
			at = now
		}
		logs = append(logs, model.AuditLog{
			SubscriptionID: r.UserID, UserID: uid, ServerType: typ, ServerID: serverID, RuleID: rule.ID,
			Action: r.Action, Target: clip(r.Target, 255), Network: clip(r.Network, 8), IP: clip(r.IP, 64),
			Count: count, HitAt: at, CreatedAt: now,
		})
		h := hits[rule.ID]
		h.n += count
		h.last = max(h.last, at)
		hits[rule.ID] = h
		if _, ok := bans[uid]; !ok && r.Action == model.AuditActionBan && rule.Action == model.AuditActionBan && rule.Enabled == 1 {
			bans[uid] = len(logs) - 1
		}
	}
	if err := s.ban(ctx, srv, typ, logs, bans, ruleByID); err != nil {
		return err
	}
	if len(logs) > 0 {
		if err := db.CreateInBatches(logs, 500).Error; err != nil {
			return err
		}
	}
	for id, h := range hits {
		err := db.Model(&model.AuditRule{}).Where("id = ?", id).UpdateColumns(map[string]any{
			"hit_count":   gorm.Expr("hit_count + ?", h.n),
			"last_hit_at": h.last,
		}).Error
		if err != nil {
			s.log().Warn("更新审计规则命中数失败", zap.Int64("rule_id", id), zap.Error(err))
		}
	}
	return nil
}

// ban 封禁账号（管理员与已封禁的账号跳过），实际封禁的账号把触发封禁的日志标记为已封禁。
func (s *Service) ban(ctx context.Context, srv node.Server, typ string, logs []model.AuditLog, bans map[int64]int,
	rules map[int64]*model.AuditRule) error {
	if len(bans) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(bans))
	for id := range bans {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	server := fmt.Sprintf("%s#%d %s", typ, srv.Common().ID, srv.Common().Name)
	stamp := s.now().Format("2006-01-02 15:04")
	for _, uid := range ids {
		l := &logs[bans[uid]]
		rule := rules[l.RuleID]
		var u model.User
		done := false
		err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "email", "banned", "is_admin", "remarks").Take(&u, uid).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil || u.Banned != 0 || u.IsAdmin != 0 {
				return err
			}
			note := fmt.Sprintf("[审计封禁 %s] 规则「%s」 目标 %s 节点 %s", stamp, rule.Name, l.Target, server)
			if u.Remarks != nil && *u.Remarks != "" {
				note = *u.Remarks + "\n" + note
			}
			done = true
			return tx.Model(&u).Updates(map[string]any{"banned": 1, "remarks": note}).Error
		})
		if err != nil {
			return err
		}
		if !done {
			continue
		}
		l.Banned = 1
		if s.RemoveSessions != nil {
			if err := s.RemoveSessions(ctx, uid); err != nil {
				s.log().Warn("审计封禁后删除登录会话失败", zap.Int64("user_id", uid), zap.Error(err))
			}
		}
		s.log().Warn("审计规则封禁账号", zap.Int64("user_id", uid), zap.Int64("rule_id", rule.ID), zap.String("target", l.Target), zap.String("server", server))
		if s.OnBan != nil {
			s.OnBan(ctx, BanInfo{UserID: uid, Email: u.Email, RuleName: rule.Name, Target: l.Target, Server: server})
		}
	}
	return nil
}

func uniq(ids []int64) []int64 {
	slices.Sort(ids)
	return slices.Compact(ids)
}

// ---------- 审计日志 ----------

// LogQuery 审计日志查询条件，零值表示不筛选。
type LogQuery struct {
	UserID         int64  `form:"user_id"`
	SubscriptionID int64  `form:"subscription_id"`
	RuleID         int64  `form:"rule_id"`
	Action         string `form:"action"`
	Target         string `form:"target"` // 包含匹配
	IP             string `form:"ip"`
	ServerType     string `form:"server_type"`
	ServerID       int64  `form:"server_id"`
	Banned         *int   `form:"banned"`
	Since          int64  `form:"since"`
	Until          int64  `form:"until"`
	Current        int    `form:"current"`
	PageSize       int    `form:"page_size"`
}

// LogView 审计日志与关联的邮箱、规则名、节点名（已删除时为空）。
type LogView struct {
	model.AuditLog
	Email      string `json:"email"`
	RuleName   string `json:"rule_name"`
	ServerName string `json:"server_name"`
}

// LogPage 审计日志分页结果。
type LogPage struct {
	Data  []LogView `json:"data"`
	Total int64     `json:"total"`
}

var likeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// Logs 按条件分页查询审计日志，按时间倒序。
func (s *Service) Logs(ctx context.Context, q LogQuery) (*LogPage, error) {
	db := s.DB.WithContext(ctx).Model(&model.AuditLog{})
	for col, v := range map[string]int64{"user_id": q.UserID, "subscription_id": q.SubscriptionID, "rule_id": q.RuleID, "server_id": q.ServerID} {
		if v > 0 {
			db = db.Where(col+" = ?", v)
		}
	}
	if q.Action != "" {
		db = db.Where("action = ?", q.Action)
	}
	if q.ServerType != "" {
		db = db.Where("server_type = ?", q.ServerType)
	}
	if q.IP != "" {
		db = db.Where("ip = ?", q.IP)
	}
	if q.Target != "" {
		db = db.Where("target LIKE ? ESCAPE '!'", "%"+likeEscaper.Replace(strings.ToLower(q.Target))+"%")
	}
	if q.Banned != nil {
		db = db.Where("banned = ?", *q.Banned)
	}
	if q.Since > 0 {
		db = db.Where("created_at >= ?", q.Since)
	}
	if q.Until > 0 {
		db = db.Where("created_at < ?", q.Until)
	}
	out := &LogPage{Data: []LogView{}}
	if err := db.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	cur, size := max(q.Current, 1), min(max(q.PageSize, 10), 100)
	var rows []model.AuditLog
	if err := db.Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return out, nil
	}
	var userIDs, ruleIDs []int64
	servers := map[string][]int64{}
	for _, r := range rows {
		userIDs, ruleIDs = append(userIDs, r.UserID), append(ruleIDs, r.RuleID)
		servers[r.ServerType] = append(servers[r.ServerType], r.ServerID)
	}
	dbc := s.DB.WithContext(ctx)
	var users []model.User
	if err := dbc.Select("id", "email").Where("id IN ?", uniq(userIDs)).Find(&users).Error; err != nil {
		return nil, err
	}
	emails := make(map[int64]string, len(users))
	for _, u := range users {
		emails[u.ID] = u.Email
	}
	var rules []model.AuditRule
	if err := dbc.Select("id", "name").Where("id IN ?", uniq(ruleIDs)).Find(&rules).Error; err != nil {
		return nil, err
	}
	ruleNames := make(map[int64]string, len(rules))
	for _, r := range rules {
		ruleNames[r.ID] = r.Name
	}
	serverNames := map[string]string{}
	for typ, ids := range servers {
		sv := node.New(typ)
		if sv == nil {
			continue
		}
		var named []struct {
			ID   int64
			Name string
		}
		if err := dbc.Table(sv.TableName()).Select("id", "name").Where("id IN ?", uniq(ids)).Scan(&named).Error; err != nil {
			return nil, err
		}
		for _, n := range named {
			serverNames[fmt.Sprintf("%s:%d", typ, n.ID)] = n.Name
		}
	}
	for _, r := range rows {
		out.Data = append(out.Data, LogView{
			AuditLog: r, Email: emails[r.UserID], RuleName: ruleNames[r.RuleID],
			ServerName: serverNames[fmt.Sprintf("%s:%d", r.ServerType, r.ServerID)],
		})
	}
	return out, nil
}

// CleanLogs 删除超过保留天数的审计日志，分批删除，返回删除行数。
func (s *Service) CleanLogs(ctx context.Context) (int64, error) {
	days := s.Settings.Int("audit_log_retention_days")
	if days <= 0 {
		days = 30
	}
	before := s.now().Unix() - days*86400
	var total int64
	for {
		var ids []int64
		err := s.DB.WithContext(ctx).Model(&model.AuditLog{}).
			Where("created_at < ?", before).Order("id ASC").Limit(cleanBatch).Pluck("id", &ids).Error
		if err != nil || len(ids) == 0 {
			return total, err
		}
		res := s.DB.WithContext(ctx).Where("id IN ?", ids).Delete(&model.AuditLog{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < cleanBatch {
			return total, nil
		}
	}
}
