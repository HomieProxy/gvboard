// Package machine 节点机一次性接入（Gvboard 新增，决策 D83）：管理端登记节点机并生成一次性接入码，
// 节点机用接入码换取本机专属凭据，之后按凭据获取面板下发的节点配置，凭据同时用于原有节点接口。
package machine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
)

const (
	// EnrollTTL 接入码有效期。
	EnrollTTL = 30 * time.Minute
	// authTTL 凭据校验结果在本进程缓存的时间，吊销后最迟这么久在其他实例生效。
	authTTL = 10 * time.Second
	// touchEvery 最后连接时间的最小更新间隔，避免每次请求都写库。
	touchEvery = 60
	// maxCache 缓存条目上限，大量伪造凭据请求时整体清空，避免占用内存。
	maxCache    = 10000
	tokenPrefix = "gvm_"
)

var (
	errNotFound = httpx.NotFoundErr("节点机不存在")
	errName     = httpx.Validation("名称不能为空且不超过 64 个字符")
	errLevel    = httpx.Validation("日志级别只能是 debug、info、warning、error、none")
	errDisabled = httpx.Validation("请先在系统配置“节点”中开启节点机")

	logLevels = []string{"debug", "info", "warning", "error", "none"}
)

// Service 节点机服务。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Log      *zap.Logger
	// Now 为 nil 时使用 time.Now，测试可替换。
	Now func() time.Time
	// HTTP 与 ReleasesURL 用于查询可更新的 v2node 版本，为空时使用默认值，测试可替换。
	HTTP        *http.Client
	ReleasesURL string

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	m  *model.NodeMachine
	at time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

// Enabled 是否开启节点机（系统配置 node_machine_enable）。
func (s *Service) Enabled() bool { return s.Settings.Bool("node_machine_enable") }

func hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// View 管理端列表项。
type View struct {
	model.NodeMachine
	// Enrolled 已接入（持有有效凭据）。
	Enrolled bool `json:"enrolled"`
	// Online 最近在拉取间隔的 3 倍（至少 3 分钟）内连接过。
	Online bool `json:"online"`
}

// Input 新建或修改节点机。
type Input struct {
	Name      string  `json:"name"`
	ServerIDs []int64 `json:"server_ids"`
	AuditIDs  []int64 `json:"audit_ids"`
	LogLevel  string  `json:"log_level"`
}

// Created 新建或重新生成接入码的结果，接入码只在此时返回一次。
type Created struct {
	Machine   *model.NodeMachine `json:"machine"`
	Code      string             `json:"code"`
	ExpiresAt int64              `json:"expires_at"`
}

func (s *Service) view(m model.NodeMachine) View {
	v := View{NodeMachine: m, Enrolled: m.TokenHash != nil}
	if m.LastSeenAt != nil {
		window := max(s.Settings.Int("server_pull_interval")*3, 180)
		v.Online = s.now().Unix()-*m.LastSeenAt <= window
	}
	if v.ServerIDs == nil {
		v.ServerIDs = model.IntList{}
	}
	if v.AuditIDs == nil {
		v.AuditIDs = model.IntList{}
	}
	return v
}

// List 全部节点机。
func (s *Service) List(ctx context.Context) ([]View, error) {
	var list []model.NodeMachine
	if err := s.DB.WithContext(ctx).Order("id").Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, m := range list {
		out = append(out, s.view(m))
	}
	return out, nil
}

func (s *Service) find(ctx context.Context, id int64) (*model.NodeMachine, error) {
	var m model.NodeMachine
	err := s.DB.WithContext(ctx).First(&m, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	return &m, err
}

// normalize 校验输入：节点必须是已存在的 v2node 节点且不属于其他节点机，审计节点必须在运行节点中。
func (s *Service) normalize(ctx context.Context, id int64, in Input) (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		return in, errName
	}
	if in.LogLevel == "" {
		in.LogLevel = "info"
	}
	if !slices.Contains(logLevels, in.LogLevel) {
		return in, errLevel
	}
	in.ServerIDs = dedupe(in.ServerIDs)
	if len(in.ServerIDs) > 0 {
		var n int64
		if err := s.DB.WithContext(ctx).Model(&model.ServerV2node{}).Where("id IN ?", in.ServerIDs).Count(&n).Error; err != nil {
			return in, err
		}
		if int(n) != len(in.ServerIDs) {
			return in, httpx.Validation("所选节点不存在或不是 v2node 节点")
		}
		var others []model.NodeMachine
		if err := s.DB.WithContext(ctx).Where("id <> ?", id).Find(&others).Error; err != nil {
			return in, err
		}
		for _, o := range others {
			for _, sid := range in.ServerIDs {
				if o.ServerIDs.Contains(sid) {
					return in, httpx.Validation(fmt.Sprintf("节点 %d 已属于节点机「%s」", sid, o.Name))
				}
			}
		}
	}
	audit := in.AuditIDs[:0:0]
	for _, aid := range dedupe(in.AuditIDs) {
		if slices.Contains(in.ServerIDs, aid) {
			audit = append(audit, aid)
		}
	}
	in.AuditIDs = audit
	return in, nil
}

func dedupe(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// newCode 生成接入码，返回明文、哈希与过期时间。
func (s *Service) newCode() (string, string, int64) {
	code := random(16)
	return code, hash(code), s.now().Add(EnrollTTL).Unix()
}

// Create 新建节点机并生成接入码。
func (s *Service) Create(ctx context.Context, in Input) (*Created, error) {
	if !s.Enabled() {
		return nil, errDisabled
	}
	in, err := s.normalize(ctx, 0, in)
	if err != nil {
		return nil, err
	}
	code, h, exp := s.newCode()
	m := model.NodeMachine{
		Name: in.Name, ServerIDs: in.ServerIDs, AuditIDs: in.AuditIDs, LogLevel: in.LogLevel,
		EnrollHash: &h, EnrollExpiresAt: &exp,
	}
	if err := s.DB.WithContext(ctx).Create(&m).Error; err != nil {
		return nil, err
	}
	return &Created{Machine: &m, Code: code, ExpiresAt: exp}, nil
}

// Update 修改名称、节点、审计与日志级别，节点机下次拉取配置时生效。
func (s *Service) Update(ctx context.Context, id int64, in Input) (*View, error) {
	m, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if in, err = s.normalize(ctx, id, in); err != nil {
		return nil, err
	}
	err = s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(map[string]any{
		"name": in.Name, "server_ids": model.IntList(in.ServerIDs), "audit_ids": model.IntList(in.AuditIDs), "log_level": in.LogLevel,
	}).Error
	if err != nil {
		return nil, err
	}
	s.forget(m)
	if m, err = s.find(ctx, id); err != nil {
		return nil, err
	}
	v := s.view(*m)
	return &v, nil
}

// Reenroll 重新生成接入码，同时吊销现有凭据（用于重装系统或更换服务器）。
func (s *Service) Reenroll(ctx context.Context, id int64) (*Created, error) {
	if !s.Enabled() {
		return nil, errDisabled
	}
	m, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	code, h, exp := s.newCode()
	err = s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(map[string]any{
		"token_hash": nil, "enroll_hash": h, "enroll_expires_at": exp,
	}).Error
	if err != nil {
		return nil, err
	}
	s.forget(m)
	m.TokenHash, m.EnrollHash, m.EnrollExpiresAt = nil, &h, &exp
	return &Created{Machine: m, Code: code, ExpiresAt: exp}, nil
}

// Revoke 吊销凭据并作废未使用的接入码，节点机随后停止全部节点服务。
func (s *Service) Revoke(ctx context.Context, id int64) error {
	m, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	err = s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(map[string]any{
		"token_hash": nil, "enroll_hash": nil, "enroll_expires_at": nil,
	}).Error
	s.forget(m)
	return err
}

// Delete 删除节点机，凭据随之失效。
func (s *Service) Delete(ctx context.Context, id int64) error {
	m, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	err = s.DB.WithContext(ctx).Delete(m).Error
	s.forget(m)
	return err
}

// forget 清除本进程缓存，m 必须是修改前读取的记录（GORM 的 Model(m).Updates 会改写 m）。
func (s *Service) forget(m *model.NodeMachine) {
	if m.TokenHash == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, *m.TokenHash)
	s.mu.Unlock()
}

// Enrolled 接入成功返回的凭据，只返回这一次。
type Enrolled struct {
	MachineID int64  `json:"machine_id"`
	Token     string `json:"token"`
}

// Enroll 用一次性接入码换取凭据。接入码无效、过期、已使用或未开启节点机时返回 nil。
func (s *Service) Enroll(ctx context.Context, code, hostname, version, ip string) (*Enrolled, error) {
	if !s.Enabled() || code == "" {
		return nil, nil
	}
	var m model.NodeMachine
	h := hash(code)
	now := s.now().Unix()
	err := s.DB.WithContext(ctx).Where("enroll_hash = ? AND enroll_expires_at >= ?", h, now).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	token := tokenPrefix + random(32)
	th := hash(token)
	// 以接入码哈希为条件更新，并发使用同一接入码时只有一个成功。
	res := s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ? AND enroll_hash = ?", m.ID, h).Updates(map[string]any{
		"token_hash": th, "enroll_hash": nil, "enroll_expires_at": nil, "enrolled_at": now,
		"hostname": clip(hostname, 255), "version": clip(version, 64), "ip": clip(ip, 64), "last_seen_at": now,
	})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	s.log().Info("节点机已接入", zap.Int64("machine_id", m.ID), zap.String("ip", ip))
	return &Enrolled{MachineID: m.ID, Token: token}, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Auth 校验节点机凭据，无效或未开启节点机时返回 nil。结果在本进程缓存 authTTL。
func (s *Service) Auth(ctx context.Context, token string) *model.NodeMachine {
	if !strings.HasPrefix(token, tokenPrefix) || !s.Enabled() {
		return nil
	}
	h := hash(token)
	now := s.now()
	s.mu.Lock()
	if c, ok := s.cache[h]; ok && now.Sub(c.at) < authTTL {
		s.mu.Unlock()
		if c.m == nil {
			return nil
		}
		cp := *c.m
		return &cp
	}
	s.mu.Unlock()
	var m model.NodeMachine
	err := s.DB.WithContext(ctx).Where("token_hash = ?", h).First(&m).Error
	var out *model.NodeMachine
	switch {
	case err == nil:
		cp := m
		out = &cp
	case !errors.Is(err, gorm.ErrRecordNotFound):
		s.log().Warn("校验节点机凭据失败", zap.Error(err))
		return nil
	}
	s.mu.Lock()
	if s.cache == nil || len(s.cache) >= maxCache {
		s.cache = map[string]cached{}
	}
	s.cache[h] = cached{m: out, at: now}
	s.mu.Unlock()
	if out == nil {
		return nil
	}
	return &m
}

// NodeConfig 下发给节点机的单个节点。
type NodeConfig struct {
	NodeID      int64 `json:"node_id"`
	AuditEnable bool  `json:"audit_enable"`
}

// Config 下发给节点机的配置。
type Config struct {
	LogLevel     string       `json:"log_level"`
	PullInterval int64        `json:"pull_interval"`
	Nodes        []NodeConfig `json:"nodes"`
	// Update 待执行的 v2node 更新，没有时省略。
	Update *Update `json:"update,omitempty"`
}

// Config 节点机配置，只包含仍存在的 v2node 节点，并顺带更新最后连接信息与更新结果。
func (s *Service) Config(ctx context.Context, m *model.NodeMachine, ip, version string, rep Report) (*Config, error) {
	cfg := &Config{LogLevel: m.LogLevel, PullInterval: s.Settings.Int("server_pull_interval"), Nodes: []NodeConfig{}}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if len(m.ServerIDs) > 0 {
		var ids []int64
		if err := s.DB.WithContext(ctx).Model(&model.ServerV2node{}).Where("id IN ?", []int64(m.ServerIDs)).Order("id").Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			cfg.Nodes = append(cfg.Nodes, NodeConfig{NodeID: id, AuditEnable: m.AuditIDs.Contains(id)})
		}
	}
	s.touch(ctx, m, ip, version)
	s.trackUpdate(ctx, m, clip(version, 64), rep)
	if m.UpdateVersion != "" {
		cfg.Update = &Update{Version: m.UpdateVersion, ID: m.UpdateID}
	}
	return cfg, nil
}

func (s *Service) touch(ctx context.Context, m *model.NodeMachine, ip, version string) {
	now := s.now().Unix()
	ip, version = clip(ip, 64), clip(version, 64)
	if m.LastSeenAt != nil && now-*m.LastSeenAt < touchEvery && m.IP == ip && (version == "" || m.Version == version) {
		return
	}
	values := map[string]any{"last_seen_at": now, "ip": ip}
	if version != "" {
		values["version"] = version
	}
	if err := s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(values).Error; err != nil {
		s.log().Warn("更新节点机最后连接时间失败", zap.Error(err))
		return
	}
	m.LastSeenAt, m.IP = &now, ip
	if version != "" {
		m.Version = version
	}
}
