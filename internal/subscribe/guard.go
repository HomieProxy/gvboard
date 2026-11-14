package subscribe

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
)

// Access 订阅请求方信息，用于风控匹配与请求日志（决策 D46）。
type Access struct {
	IP        string
	UserAgent string
	// Flag flag 参数原值（未传时为空，不回退到 User-Agent）。
	Flag string
	// Header 读取请求头，为 nil 时视为没有请求头。
	Header func(name string) string
	// Token、Path 只用于请求日志。
	Token string
	Path  string
}

func (a Access) header(name string) string {
	if a.Header == nil {
		return ""
	}
	return a.Header(name)
}

// clientApp App 内置配置接口在规则与日志中的客户端名称。
const clientApp = "App"

const (
	rulePatternMaxLines = 200
	rulePatternMaxLen   = 10000
	ruleRegexMaxLen     = 1000
	// ruleCheckInterval 各实例检查规则版本的间隔，规则修改后最迟在该时间后生效。
	ruleCheckInterval = 5 * time.Second
	// shareMaxIPs 防分享记录的 IP 上限，防止仅记录模式下集合无限增长。
	shareMaxIPs = 10000
)

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// compiledRule 预处理后的规则：文本值转小写，正则与 IP 段已解析。
type compiledRule struct {
	model.SubscribeRule
	values []string
	re     *regexp.Regexp
	nets   []netip.Prefix
}

// RuleInput 管理端提交的规则。Pattern 多个值按行分隔，任一匹配即命中；正则只填一个表达式。
type RuleInput struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Header    string `json:"header"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Negate    bool   `json:"negate"`
	Action    string `json:"action"`
	Priority  int64  `json:"priority"`
	Enabled   bool   `json:"enabled"`
}

func ruleErr(msg string) error { return httpx.Validation(msg) }

// compileRule 校验并预处理规则，管理端保存与运行时加载使用同一套规则。
func compileRule(r model.SubscribeRule) (*compiledRule, error) {
	if utf8.RuneCountInString(r.Name) > 64 {
		return nil, ruleErr("规则名称不能超过 64 个字符")
	}
	switch r.Target {
	case model.RuleTargetUA, model.RuleTargetIP, model.RuleTargetFlag, model.RuleTargetClient:
		r.Header = ""
	case model.RuleTargetHeader:
		if !headerName.MatchString(r.Header) {
			return nil, ruleErr("请求头名称不合法")
		}
	default:
		return nil, ruleErr("匹配对象不正确")
	}
	switch r.Action {
	case model.RuleActionAllow, model.RuleActionLog, model.RuleActionEmpty, model.RuleActionDeny:
	default:
		return nil, ruleErr("动作不正确")
	}
	if r.Priority < -1000000 || r.Priority > 1000000 {
		return nil, ruleErr("优先级取值范围为 -1000000～1000000")
	}
	pattern := ""
	if r.Pattern != nil {
		pattern = strings.TrimSpace(*r.Pattern)
	}
	if len(pattern) > rulePatternMaxLen {
		return nil, ruleErr("匹配值过长")
	}
	c := &compiledRule{}
	switch r.MatchType {
	case model.RuleMatchEmpty:
		if r.Target == model.RuleTargetIP {
			return nil, ruleErr("IP 只能按 IP / CIDR 匹配")
		}
		pattern = ""
	case model.RuleMatchRegex:
		if r.Target == model.RuleTargetIP {
			return nil, ruleErr("IP 只能按 IP / CIDR 匹配")
		}
		if pattern == "" || len(pattern) > ruleRegexMaxLen {
			return nil, ruleErr("正则表达式不能为空且不能超过 1000 个字符")
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return nil, ruleErr("正则表达式不正确")
		}
		c.re = re
	case model.RuleMatchCIDR:
		if r.Target != model.RuleTargetIP {
			return nil, ruleErr("只有 IP 可以按 IP / CIDR 匹配")
		}
		lines := splitLines(pattern)
		if len(lines) == 0 || len(lines) > rulePatternMaxLines {
			return nil, ruleErr("请填写 1～200 个 IP 或 CIDR")
		}
		for _, l := range lines {
			p, err := parsePrefix(l)
			if err != nil {
				return nil, ruleErr(fmt.Sprintf("IP 或 CIDR 不正确：%s", l))
			}
			c.nets = append(c.nets, p)
		}
		pattern = strings.Join(lines, "\n")
	case model.RuleMatchContains, model.RuleMatchEquals, model.RuleMatchPrefix:
		if r.Target == model.RuleTargetIP {
			return nil, ruleErr("IP 只能按 IP / CIDR 匹配")
		}
		lines := splitLines(pattern)
		if len(lines) == 0 || len(lines) > rulePatternMaxLines {
			return nil, ruleErr("请填写 1～200 个匹配值")
		}
		for _, l := range lines {
			c.values = append(c.values, strings.ToLower(l))
		}
		pattern = strings.Join(lines, "\n")
	default:
		return nil, ruleErr("匹配方式不正确")
	}
	if pattern == "" {
		r.Pattern = nil
	} else {
		r.Pattern = &pattern
	}
	c.SubscribeRule = r
	return c, nil
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return p, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (r *compiledRule) match(a Access, client string) bool {
	var subject string
	switch r.Target {
	case model.RuleTargetUA:
		subject = a.UserAgent
	case model.RuleTargetHeader:
		subject = a.header(r.Header)
	case model.RuleTargetIP:
		subject = a.IP
	case model.RuleTargetFlag:
		subject = a.Flag
	case model.RuleTargetClient:
		subject = client
	}
	hit := false
	switch r.MatchType {
	case model.RuleMatchEmpty:
		hit = subject == ""
	case model.RuleMatchRegex:
		hit = r.re.MatchString(subject)
	case model.RuleMatchCIDR:
		if addr, err := netip.ParseAddr(subject); err == nil {
			addr = addr.Unmap()
			for _, p := range r.nets {
				if p.Contains(addr) {
					hit = true
					break
				}
			}
		}
	default:
		s := strings.ToLower(subject)
		for _, v := range r.values {
			switch r.MatchType {
			case model.RuleMatchContains:
				hit = strings.Contains(s, v)
			case model.RuleMatchEquals:
				hit = s == v
			case model.RuleMatchPrefix:
				hit = strings.HasPrefix(s, v)
			}
			if hit {
				break
			}
		}
	}
	return hit != (r.Negate == 1)
}

// ruleCache 启用规则的进程内缓存，按 Redis 中的版本号判断是否需要重新加载。
type ruleCache struct {
	mu      sync.Mutex
	rules   []*compiledRule
	version string
	loaded  bool
	checked time.Time
}

func (s *Service) activeRules(ctx context.Context) []*compiledRule {
	rc := &s.rules
	rc.mu.Lock()
	defer rc.mu.Unlock()
	now := time.Now()
	if rc.loaded && now.Sub(rc.checked) < ruleCheckInterval {
		return rc.rules
	}
	version, err := s.Cache.Get(ctx, s.Cache.KeyOf(cache.KeySubscribeRuleVersion, nil)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		s.log().Warn("读取订阅风控规则版本失败", zap.Error(err))
		if rc.loaded {
			rc.checked = now
			return rc.rules
		}
	}
	if rc.loaded && version == rc.version {
		rc.checked = now
		return rc.rules
	}
	var rows []model.SubscribeRule
	if err := s.DB.WithContext(ctx).Where("enabled = ?", 1).Order("priority ASC, id ASC").Find(&rows).Error; err != nil {
		s.log().Warn("加载订阅风控规则失败", zap.Error(err))
		return rc.rules
	}
	list := make([]*compiledRule, 0, len(rows))
	for _, r := range rows {
		c, err := compileRule(r)
		if err != nil {
			s.log().Warn("订阅风控规则无效，已跳过", zap.Int64("rule_id", r.ID), zap.Error(err))
			continue
		}
		list = append(list, c)
	}
	rc.rules, rc.version, rc.loaded, rc.checked = list, version, true, now
	return list
}

// invalidateRules 规则修改后通知所有实例重新加载。
func (s *Service) invalidateRules(ctx context.Context) {
	s.rules.mu.Lock()
	s.rules.loaded = false
	s.rules.mu.Unlock()
	if err := s.Cache.Incr(ctx, s.Cache.KeyOf(cache.KeySubscribeRuleVersion, nil)).Err(); err != nil {
		s.log().Warn("更新订阅风控规则版本失败", zap.Error(err))
	}
}

// verdict 风控判定结果。
type verdict struct {
	allow  bool   // 命中放行规则，跳过频率限制与防分享
	action string // empty / deny，为空表示继续下发
	result int
	ruleID *int64
}

// evaluate 按优先级匹配规则：只记录的规则继续匹配，其余动作命中即结束。返回命中的全部规则 ID。
func evaluate(rules []*compiledRule, a Access, client string) (v verdict, hits []int64) {
	for _, r := range rules {
		if !r.match(a, client) {
			continue
		}
		id := r.ID
		hits = append(hits, id)
		switch r.Action {
		case model.RuleActionLog:
			if v.ruleID == nil {
				v.ruleID = &id
			}
			continue
		case model.RuleActionAllow:
			v.allow = true
		case model.RuleActionEmpty:
			v.action, v.result = model.RuleActionEmpty, model.SubscribeResultRuleEmpty
		case model.RuleActionDeny:
			v.action, v.result = model.RuleActionDeny, model.SubscribeResultRuleDeny
		}
		v.ruleID = &id
		return v, hits
	}
	return v, hits
}

// rateLimited 固定窗口计数：单订阅或单 IP 在窗口内的请求次数超过上限。Redis 异常时放行。
func (s *Service) rateLimited(ctx context.Context, subID int64, ip string) bool {
	subLimit := s.Settings.Int("subscribe_rate_sub_limit")
	ipLimit := s.Settings.Int("subscribe_rate_ip_limit")
	if subLimit <= 0 && ipLimit <= 0 {
		return false
	}
	window := s.Settings.Int("subscribe_rate_window")
	if window <= 0 {
		window = 3600
	}
	slot := s.now().Unix() / window
	ttl := time.Duration(window+60) * time.Second
	pipe := s.Cache.TxPipeline()
	var subN, ipN *redis.IntCmd
	if subLimit > 0 {
		k := s.Cache.KeyOf(cache.KeySubscribeRateSub, fmt.Sprintf("%d:%d", subID, slot))
		subN = pipe.Incr(ctx, k)
		pipe.Expire(ctx, k, ttl)
	}
	if ipLimit > 0 && ip != "" {
		k := s.Cache.KeyOf(cache.KeySubscribeRateIP, fmt.Sprintf("%s:%d", ip, slot))
		ipN = pipe.Incr(ctx, k)
		pipe.Expire(ctx, k, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.log().Warn("订阅频率限制计数失败", zap.Error(err))
		return false
	}
	return (subN != nil && subN.Val() > subLimit) || (ipN != nil && ipN.Val() > ipLimit)
}

// shareScript 记录订阅最近的请求 IP 并判断是否超过上限：已记录的 IP 始终放行；
// 新 IP 超过上限时返回 1，拦截模式（ARGV[5] = 1）下不记录该 IP，保证已有设备不受影响。
var shareScript = redis.NewScript(`
local key = KEYS[1]
local now, since, ip = tonumber(ARGV[1]), tonumber(ARGV[2]), ARGV[3]
local limit, block, ttl, cap = tonumber(ARGV[4]), ARGV[5], tonumber(ARGV[6]), tonumber(ARGV[7])
redis.call('ZREMRANGEBYSCORE', key, '-inf', since)
if redis.call('ZSCORE', key, ip) then
  redis.call('ZADD', key, now, ip)
  redis.call('EXPIRE', key, ttl)
  return 0
end
local n = redis.call('ZCARD', key)
local over = 0
if n >= limit then
  over = 1
  if block == '1' then return 1 end
end
if n < cap then
  redis.call('ZADD', key, now, ip)
  redis.call('EXPIRE', key, ttl)
end
return over
`)

// shareExceeded 防分享：订阅在窗口内出现的不同 IP 数超过上限。返回是否超限与配置的动作。Redis 异常时放行。
func (s *Service) shareExceeded(ctx context.Context, subID int64, ip string) (bool, string) {
	limit := s.Settings.Int("subscribe_share_ip_limit")
	if limit <= 0 || ip == "" {
		return false, ""
	}
	hours := s.Settings.Int("subscribe_share_window")
	if hours <= 0 {
		hours = 24
	}
	action := s.Settings.String("subscribe_share_action")
	block := "0"
	if action == model.RuleActionEmpty || action == model.RuleActionDeny {
		block = "1"
	}
	now := s.now().Unix()
	window := hours * 3600
	n, err := shareScript.Run(ctx, s.Cache, []string{s.Cache.KeyOf(cache.KeySubscribeShare, subID)},
		now, now-window, ip, limit, block, window, shareMaxIPs).Int()
	if err != nil {
		s.log().Warn("订阅防分享检查失败", zap.Error(err))
		return false, ""
	}
	return n == 1, action
}

// ShareIPs 订阅在防分享窗口内出现过的 IP 数（管理端展示用）。
func (s *Service) ShareIPs(ctx context.Context, subID int64) (int64, error) {
	hours := s.Settings.Int("subscribe_share_window")
	if hours <= 0 {
		hours = 24
	}
	since := s.now().Unix() - hours*3600
	return s.Cache.ZCount(ctx, s.Cache.KeyOf(cache.KeySubscribeShare, subID), "("+strconv.FormatInt(since, 10), "+inf").Result()
}
