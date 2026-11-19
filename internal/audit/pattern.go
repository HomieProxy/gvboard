// Package audit 审计规则：节点按规则记录、阻断或封禁访问指定网站的用户（决策 D81）。
package audit

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// 匹配内容的种类。v2node gvboard 分支使用相同的语法。
const (
	kindDomain  = "domain"  // 域名及其子域名
	kindFull    = "full"    // 完整域名
	kindKeyword = "keyword" // 域名包含
	kindRegexp  = "regexp"  // 域名正则
	kindIP      = "ip"      // IP 或 IP 段
	kindPort    = "port"    // 只匹配端口
)

const (
	maxLines     = 2000
	maxLineLen   = 1000
	maxDomainLen = 253
)

var domainChars = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)*\.?$`)

// entry 一行匹配内容。portLo 为 0 表示不限端口。
type entry struct {
	kind   string
	value  string
	re     *regexp.Regexp
	prefix netip.Prefix
	portLo uint16
	portHi uint16
}

// Target 一次访问的目标。Domain 为空表示不知道域名，IP 无效表示不知道 IP。
type Target struct {
	Domain string
	IP     netip.Addr
	Port   uint16
}

// ParseTarget 解析管理端测试输入：example.com:443、1.2.3.4:443、[::1]:443、example.com、1.2.3.4。
func ParseTarget(s string) (Target, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	var t Target
	host, port, err := splitHostPort(s)
	if err != nil {
		return t, err
	}
	if port != "" {
		lo, hi, err := parsePorts(port)
		if err != nil || lo != hi {
			return t, fmt.Errorf("端口不正确：%s", port)
		}
		t.Port = lo
	}
	if host == "" {
		return t, fmt.Errorf("请填写域名或 IP")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		t.IP = a.Unmap()
		return t, nil
	}
	t.Domain = strings.TrimSuffix(host, ".")
	return t, nil
}

func (e *entry) match(t Target) bool {
	if e.portLo > 0 && (t.Port < e.portLo || t.Port > e.portHi) {
		return false
	}
	switch e.kind {
	case kindPort:
		return true
	case kindIP:
		return t.IP.IsValid() && e.prefix.Contains(t.IP.Unmap())
	}
	if t.Domain == "" {
		return false
	}
	switch e.kind {
	case kindDomain:
		return t.Domain == e.value || strings.HasSuffix(t.Domain, "."+e.value)
	case kindFull:
		return t.Domain == e.value
	case kindKeyword:
		return strings.Contains(t.Domain, e.value)
	case kindRegexp:
		return e.re.MatchString(t.Domain)
	}
	return false
}

// parsePattern 解析规则的匹配内容，返回解析结果与去掉空行、注释后的规范化行（下发给节点）。
func parsePattern(pattern string) ([]*entry, []string, error) {
	var entries []*entry
	var lines []string
	for _, raw := range strings.Split(pattern, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if len(l) > maxLineLen {
			return nil, nil, fmt.Errorf("单行不能超过 %d 个字符", maxLineLen)
		}
		e, norm, err := parseLine(l)
		if err != nil {
			return nil, nil, fmt.Errorf("%s：%w", l, err)
		}
		entries = append(entries, e)
		lines = append(lines, norm)
		if len(lines) > maxLines {
			return nil, nil, fmt.Errorf("匹配内容不能超过 %d 行", maxLines)
		}
	}
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("请填写匹配内容")
	}
	return entries, lines, nil
}

// parseLine 解析一行，返回规范化后的写法。
func parseLine(l string) (*entry, string, error) {
	if v, ok := cutPrefixFold(l, "regexp:"); ok {
		re, err := regexp.Compile("(?i)" + v)
		if v == "" || err != nil {
			return nil, "", fmt.Errorf("正则表达式不正确")
		}
		return &entry{kind: kindRegexp, value: v, re: re}, "regexp:" + v, nil
	}
	l = strings.ToLower(l)
	if v, ok := strings.CutPrefix(l, "keyword:"); ok {
		if v == "" || strings.ContainsAny(v, " \t/") {
			return nil, "", fmt.Errorf("关键字不正确")
		}
		return &entry{kind: kindKeyword, value: v}, "keyword:" + v, nil
	}
	kind := ""
	if v, ok := strings.CutPrefix(l, "full:"); ok {
		kind, l = kindFull, v
	} else if v, ok := strings.CutPrefix(l, "domain:"); ok {
		kind, l = kindDomain, v
	}
	host, port, err := splitHostPort(l)
	if err != nil {
		return nil, "", err
	}
	e := &entry{}
	if port != "" {
		if e.portLo, e.portHi, err = parsePorts(port); err != nil {
			return nil, "", err
		}
	}
	portSuffix := ""
	if port != "" {
		portSuffix = ":" + port
	}
	if host == "" {
		if kind != "" || port == "" {
			return nil, "", fmt.Errorf("格式不正确")
		}
		e.kind = kindPort
		return e, portSuffix, nil
	}
	if kind == "" {
		if p, ok := parseIPPrefix(host); ok {
			e.kind, e.prefix = kindIP, p
			h := p.String()
			if p.IsSingleIP() {
				h = p.Addr().String()
			}
			if p.Addr().Is6() && port != "" {
				h = "[" + h + "]"
			}
			return e, h + portSuffix, nil
		}
		kind = kindDomain
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "*."), ".")
	if host == "" || len(host) > maxDomainLen || !domainChars.MatchString(host) {
		return nil, "", fmt.Errorf("域名不正确")
	}
	e.kind, e.value = kind, host
	norm := host
	if kind == kindFull {
		norm = "full:" + host
	}
	return e, norm + portSuffix, nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// splitHostPort 拆分主机与端口：[IPv6]:端口、主机:端口、不带端口的 IPv6（含多个冒号）。
func splitHostPort(s string) (host, port string, err error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("缺少 ]")
		}
		host, rest := s[1:end], s[end+1:]
		if rest == "" {
			return host, "", nil
		}
		if !strings.HasPrefix(rest, ":") || len(rest) == 1 {
			return "", "", fmt.Errorf("格式不正确")
		}
		return host, rest[1:], nil
	}
	switch strings.Count(s, ":") {
	case 0:
		return s, "", nil
	case 1:
		host, port, _ = strings.Cut(s, ":")
		if port == "" {
			return "", "", fmt.Errorf("缺少端口")
		}
		return host, port, nil
	}
	return s, "", nil
}

// parsePorts 解析端口或端口段（6881-6889）。
func parsePorts(s string) (uint16, uint16, error) {
	lo, hi, isRange := strings.Cut(s, "-")
	a, err1 := strconv.ParseUint(lo, 10, 16)
	b := a
	var err2 error
	if isRange {
		b, err2 = strconv.ParseUint(hi, 10, 16)
	}
	if err1 != nil || err2 != nil || a == 0 || b == 0 || a > b {
		return 0, 0, fmt.Errorf("端口不正确：%s", s)
	}
	return uint16(a), uint16(b), nil
}

func parseIPPrefix(s string) (netip.Prefix, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return p, false
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), p.Bits() >= 0
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}
