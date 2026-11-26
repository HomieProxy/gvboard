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

// BanInfo 审计封禁或告警的信息，用于通知管理员。
type BanInfo struct {
	UserID   int64
	Email    string
	RuleName string
	// Action 节点实际执行的动作（log / block / ban）。
	Action string
	Target string
	Server string
}

// Service 审计规则服务。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Log      *zap.Logger
	Now      func() time.Time
	// RemoveSessions 删除用户全部登录会话（封禁时调用），可为 nil。
	RemoveSessions func(ctx context.Context, userID int64) error
	// OnBan 开启告警的规则封禁账号后调用（通知管理员），可为 nil。
	OnBan func(ctx context.Context, info BanInfo)
	// OnAlert 开启告警的规则命中后调用（通知管理员），同一订阅、规则每个自然日一次；
	// 本次已发封禁通知的不再调用。可为 nil。
	OnAlert func(ctx context.Context, info BanInfo)
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
	Alert   bool    `json:"alert"`
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
	enabled, alert := 0, 0
	if in.Enabled {
		enabled = 1
	}
	if in.Alert {
		alert = 1
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
	r.Name, r.Pattern, r.Action, r.GroupID, r.Enabled, r.Alert, r.Sort = name, strings.TrimSpace(in.Pattern), in.Action, groups, enabled, alert, in.Sort
	if id > 0 {
		err := db.Model(r).Select("name", "pattern", "action", "group_id", "enabled", "alert", "sort", "updated_at").Updates(r).Error
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

// dayStart 返回 ts 所在自然日（服务器本地时区）的零点 Unix 秒。
func (s *Service) dayStart(ts int64) int64 {
	loc := s.now().Location()
	t := time.Unix(ts, 0).In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix()
}

func actionRank(a string) int {
	switch a {
	case model.AuditActionBan:
		return 3
	case model.AuditActionBlock:
		return 2
	case model.AuditActionLog:
		return 1
	default:
		return 0
	}
}

// banTrig 待封禁：账号 → 触发用的汇总日志与具体目标。
type banTrig struct {
	logID  int64
	ruleID int64
	target string
}

// Report 处理节点上报：按「订阅 + 规则 + 节点 + 自然日」合并写入汇总与细节，累计规则命中数；
// 规则当前动作为封禁、且节点已按封禁处理时封禁账号；规则开启告警时同一订阅、规则每个自然日通知管理员一次。
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
	bans := map[int64]banTrig{} // 账号 → 触发封禁信息
	alerts := []alertTrig{}
	checked := map[[3]int64]bool{} // 订阅、规则、自然日 → 本次已检查过是否告警
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
		target, network, ip := clip(r.Target, 255), clip(r.Network, 8), clip(r.IP, 64)
		day := s.dayStart(at)
		var sum model.AuditLog
		err := db.Where("subscription_id = ? AND rule_id = ? AND server_type = ? AND server_id = ? AND day = ?",
			r.UserID, rule.ID, typ, serverID, day).Order("id ASC").Take(&sum).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sum = model.AuditLog{
				SubscriptionID: r.UserID, UserID: uid, ServerType: typ, ServerID: serverID, RuleID: rule.ID,
				Action: r.Action, Target: target, Network: network, IP: ip, Count: 0, DetailCount: 0,
				Day: day, HitAt: at, CreatedAt: now,
			}
			if err := db.Create(&sum).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		var det model.AuditLogDetail
		err = db.Where("log_id = ? AND target = ? AND network = ?", sum.ID, target, network).Take(&det).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			det = model.AuditLogDetail{LogID: sum.ID, Target: target, Network: network, IP: ip, Count: count, HitAt: at}
			if err := db.Create(&det).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			det.Count += count
			det.IP, det.HitAt = ip, max(det.HitAt, at)
			if err := db.Model(&det).Updates(map[string]any{"count": det.Count, "ip": det.IP, "hit_at": det.HitAt}).Error; err != nil {
				return err
			}
		}
		if err := s.refreshLog(db, sum.ID, r.Action); err != nil {
			return err
		}
		h := hits[rule.ID]
		h.n += count
		h.last = max(h.last, at)
		hits[rule.ID] = h
		if _, ok := bans[uid]; !ok && r.Action == model.AuditActionBan && rule.Action == model.AuditActionBan && rule.Enabled == 1 {
			bans[uid] = banTrig{logID: sum.ID, ruleID: rule.ID, target: target}
		}
		if key := [3]int64{r.UserID, rule.ID, day}; rule.Alert == 1 && rule.Enabled == 1 && !checked[key] {
			checked[key] = true
			var n int64
			if err := db.Model(&model.AuditLog{}).Where("subscription_id = ? AND rule_id = ? AND day = ? AND alerted = ?",
				r.UserID, rule.ID, day, 1).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				alerts = append(alerts, alertTrig{userID: uid, logID: sum.ID, ruleID: rule.ID, action: r.Action, target: target})
			}
		}
	}
	notified, err := s.ban(ctx, srv, typ, bans, ruleByID)
	if err != nil {
		return err
	}
	if err := s.alert(ctx, srv, typ, alerts, notified, ruleByID); err != nil {
		return err
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

// refreshLog 按细节重算汇总的次数、目标、动作与细节数。
func (s *Service) refreshLog(db *gorm.DB, logID int64, incomingAction string) error {
	var details []model.AuditLogDetail
	if err := db.Where("log_id = ?", logID).Find(&details).Error; err != nil {
		return err
	}
	var sum model.AuditLog
	if err := db.Take(&sum, logID).Error; err != nil {
		return err
	}
	var total, hitAt int64
	top := model.AuditLogDetail{}
	for _, d := range details {
		total += d.Count
		hitAt = max(hitAt, d.HitAt)
		if d.Count > top.Count || (d.Count == top.Count && d.HitAt >= top.HitAt) {
			top = d
		}
	}
	action := sum.Action
	if actionRank(incomingAction) > actionRank(action) {
		action = incomingAction
	}
	return db.Model(&sum).Updates(map[string]any{
		"count": total, "detail_count": int64(len(details)), "action": action,
		"target": top.Target, "network": top.Network, "ip": top.IP, "hit_at": hitAt,
	}).Error
}

// ban 封禁账号（管理员与已封禁的账号跳过），实际封禁的账号把触发封禁的汇总日志标记为已封禁；
// 规则开启告警时通知管理员。返回已发封禁通知的 账号 → 规则。
func (s *Service) ban(ctx context.Context, srv node.Server, typ string, bans map[int64]banTrig,
	rules map[int64]*model.AuditRule) (map[int64]int64, error) {
	notified := map[int64]int64{}
	if len(bans) == 0 {
		return notified, nil
	}
	ids := make([]int64, 0, len(bans))
	for id := range bans {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	server := fmt.Sprintf("%s#%d %s", typ, srv.Common().ID, srv.Common().Name)
	stamp := s.now().Format("2006-01-02 15:04")
	for _, uid := range ids {
		trig := bans[uid]
		rule := rules[trig.ruleID]
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
			note := fmt.Sprintf("[审计封禁 %s] 规则「%s」 目标 %s 节点 %s", stamp, rule.Name, trig.target, server)
			if u.Remarks != nil && *u.Remarks != "" {
				note = *u.Remarks + "\n" + note
			}
			done = true
			return tx.Model(&u).Updates(map[string]any{"banned": 1, "remarks": note}).Error
		})
		if err != nil {
			return nil, err
		}
		if !done {
			continue
		}
		mark := map[string]any{"banned": 1}
		if rule.Alert == 1 {
			mark["alerted"] = 1
		}
		if err := s.DB.WithContext(ctx).Model(&model.AuditLog{}).Where("id = ?", trig.logID).Updates(mark).Error; err != nil {
			return nil, err
		}
		if s.RemoveSessions != nil {
			if err := s.RemoveSessions(ctx, uid); err != nil {
				s.log().Warn("审计封禁后删除登录会话失败", zap.Int64("user_id", uid), zap.Error(err))
			}
		}
		s.log().Warn("审计规则封禁账号", zap.Int64("user_id", uid), zap.Int64("rule_id", rule.ID), zap.String("target", trig.target), zap.String("server", server))
		if rule.Alert == 1 {
			notified[uid] = rule.ID
			if s.OnBan != nil {
				s.OnBan(ctx, BanInfo{UserID: uid, Email: u.Email, RuleName: rule.Name, Action: model.AuditActionBan, Target: trig.target, Server: server})
			}
		}
	}
	return notified, nil
}

// alertTrig 待告警：一个订阅当日首次命中某条开启告警的规则。
type alertTrig struct {
	userID int64
	logID  int64
	ruleID int64
	action string
	target string
}

// alert 把触发告警的汇总日志标记为已告警并通知管理员；本次已发封禁通知的（同一账号、规则）跳过。
func (s *Service) alert(ctx context.Context, srv node.Server, typ string, alerts []alertTrig,
	notified map[int64]int64, rules map[int64]*model.AuditRule) error {
	if len(alerts) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(alerts))
	for _, a := range alerts {
		ids = append(ids, a.userID)
	}
	var users []model.User
	if err := s.DB.WithContext(ctx).Select("id", "email").Where("id IN ?", uniq(ids)).Find(&users).Error; err != nil {
		return err
	}
	email := make(map[int64]string, len(users))
	for _, u := range users {
		email[u.ID] = u.Email
	}
	server := fmt.Sprintf("%s#%d %s", typ, srv.Common().ID, srv.Common().Name)
	for _, a := range alerts {
		if id, ok := notified[a.userID]; ok && id == a.ruleID {
			continue
		}
		if err := s.DB.WithContext(ctx).Model(&model.AuditLog{}).Where("id = ?", a.logID).Update("alerted", 1).Error; err != nil {
			return err
		}
		rule := rules[a.ruleID]
		s.log().Info("审计规则告警", zap.Int64("user_id", a.userID), zap.Int64("rule_id", rule.ID),
			zap.String("action", a.action), zap.String("target", a.target), zap.String("server", server))
		if s.OnAlert != nil {
			s.OnAlert(ctx, BanInfo{UserID: a.userID, Email: email[a.userID], RuleName: rule.Name, Action: a.action, Target: a.target, Server: server})
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
		db = db.Where("ip = ? OR EXISTS (SELECT 1 FROM v2_audit_log_detail d WHERE d.log_id = v2_audit_log.id AND d.ip = ?)", q.IP, q.IP)
	}
	if q.Target != "" {
		pat := "%" + likeEscaper.Replace(strings.ToLower(q.Target)) + "%"
		db = db.Where("target LIKE ? ESCAPE '!' OR EXISTS (SELECT 1 FROM v2_audit_log_detail d WHERE d.log_id = v2_audit_log.id AND d.target LIKE ? ESCAPE '!')", pat, pat)
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

// LogDetails 返回一条汇总日志下的全部细节，按最后命中时间倒序。
func (s *Service) LogDetails(ctx context.Context, logID int64) ([]model.AuditLogDetail, error) {
	if logID <= 0 {
		return nil, errInvalid
	}
	var n int64
	if err := s.DB.WithContext(ctx).Model(&model.AuditLog{}).Where("id = ?", logID).Count(&n).Error; err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, httpx.NotFoundErr("日志不存在")
	}
	out := []model.AuditLogDetail{}
	err := s.DB.WithContext(ctx).Where("log_id = ?", logID).Order("hit_at DESC, id DESC").Find(&out).Error
	return out, err
}

// CleanLogs 删除超过保留天数的审计日志（先删细节），分批删除，返回删除的汇总行数。
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
		if err := s.DB.WithContext(ctx).Where("log_id IN ?", ids).Delete(&model.AuditLogDetail{}).Error; err != nil {
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
