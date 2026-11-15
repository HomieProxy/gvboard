// Package realip 按可信代理解析客户端真实 IP，并统计“前面还有一层未被信任的代理”的请求，
// 用于提示管理员 CDN 的 IP 段没有加入可信代理。
package realip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// Resolver 真实 IP 解析器。可信代理由配置文件（启动时固定）与后台设置（运行中可修改）两部分组成。
type Resolver struct {
	static  []netip.Prefix
	dynamic atomic.Pointer[func() []string]
	cur     atomic.Pointer[dynamicSet]
	stats   stats
	now     func() time.Time
}

type dynamicSet struct {
	raw      []string
	prefixes []netip.Prefix
}

// New 创建解析器，static 为配置文件中的可信代理（IP 或 CIDR）。
func New(static []string) (*Resolver, error) {
	p, err := ParsePrefixes(static)
	if err != nil {
		return nil, err
	}
	return &Resolver{static: p, now: time.Now}, nil
}

// UseDynamic 设置运行中可修改的可信代理来源（后台设置），每次请求读取，值变化时重新解析。
func (r *Resolver) UseDynamic(f func() []string) { r.dynamic.Store(&f) }

// ParsePrefixes 解析 IP 或 CIDR 列表，单个 IP 视为 /32（IPv6 为 /128）。
func ParsePrefixes(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("IP 段 %q 格式不正确", s)
			}
			if p.Addr().Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("IP %q 格式不正确", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func (r *Resolver) dynamicPrefixes() []netip.Prefix {
	f := r.dynamic.Load()
	if f == nil {
		return nil
	}
	raw := (*f)()
	if cur := r.cur.Load(); cur != nil && slices.Equal(cur.raw, raw) {
		return cur.prefixes
	}
	// 保存时已校验，这里忽略个别无法解析的项，不影响其它项生效。
	ps := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		if p, err := ParsePrefixes([]string{s}); err == nil {
			ps = append(ps, p...)
		}
	}
	r.cur.Store(&dynamicSet{raw: slices.Clone(raw), prefixes: ps})
	return ps
}

func (r *Resolver) trusted(a netip.Addr, dyn *[]netip.Prefix, loaded *bool) bool {
	for _, p := range r.static {
		if p.Contains(a) {
			return true
		}
	}
	if !*loaded {
		*dyn, *loaded = r.dynamicPrefixes(), true
	}
	for _, p := range *dyn {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolve 返回客户端 IP。remoteAddr 是可信代理时，从 X-Forwarded-For 右侧向左跳过可信代理，
// 取第一个不可信的地址；没有 X-Forwarded-For 时使用 X-Real-IP。
// suspect 为 true 表示前面还有一层未被信任的代理：取到的地址左边还有地址，
// 或连接来自不可信地址却带有转发头。
func (r *Resolver) Resolve(remoteAddr string, h http.Header) (ip netip.Addr, suspect bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	remote = remote.Unmap()
	xff := h.Values("X-Forwarded-For")
	var dyn []netip.Prefix
	loaded := false
	if !r.trusted(remote, &dyn, &loaded) {
		return remote, len(xff) > 0
	}
	if len(xff) > 0 {
		items := strings.Split(strings.Join(xff, ","), ",")
		for i := len(items) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(items[i]))
			if err != nil {
				break
			}
			a = a.Unmap()
			if i == 0 || !r.trusted(a, &dyn, &loaded) {
				return a, i > 0
			}
		}
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(h.Get("X-Real-IP"))); err == nil {
		return a.Unmap(), false
	}
	return remote, false
}

// Middleware 解析真实 IP 并写回 Request.RemoteAddr，之后的 c.ClientIP() 都返回该地址。
// 必须是第一个中间件，且 Gin 本身不再信任任何代理。
func (r *Resolver) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip, suspect := r.Resolve(c.Request.RemoteAddr, c.Request.Header)
		r.stats.record(r.now(), ip, suspect)
		if ip.IsValid() {
			_, port, err := net.SplitHostPort(c.Request.RemoteAddr)
			if err != nil {
				port = "0"
			}
			c.Request.RemoteAddr = net.JoinHostPort(ip.String(), port)
		}
		c.Next()
	}
}

// 统计最近两个窗口（10～20 分钟）内的请求。
const (
	statWindow    = 10 * time.Minute
	maxSamples    = 5
	warnMinSusp   = 20  // 可疑请求至少这么多才提示，避免个别伪造请求头的请求触发
	warnMinRatio  = 0.3 // 可疑请求占全部请求的比例
	sampleMaxSize = 64
)

type bucket struct {
	idx            int64
	total, suspect int64
}

type stats struct {
	mu      sync.Mutex
	buckets [2]bucket
	samples []string
}

func (s *stats) record(now time.Time, ip netip.Addr, suspect bool) {
	idx := now.UnixNano() / int64(statWindow)
	s.mu.Lock()
	b := &s.buckets[idx%2]
	if b.idx != idx {
		*b = bucket{idx: idx}
	}
	b.total++
	if suspect {
		b.suspect++
		if ip.IsValid() {
			str := ip.String()
			if len(str) <= sampleMaxSize {
				s.samples = slices.DeleteFunc(s.samples, func(x string) bool { return x == str })
				s.samples = append(s.samples, str)
				if len(s.samples) > maxSamples {
					s.samples = s.samples[len(s.samples)-maxSamples:]
				}
			}
		}
	}
	s.mu.Unlock()
}

// Report 最近 10～20 分钟本实例收到的请求中，前面还有一层未被信任的代理的情况。
type Report struct {
	Total   int64    `json:"total"`   // 请求数
	Suspect int64    `json:"suspect"` // 前面还有未被信任的代理的请求数
	Warning bool     `json:"warning"` // 可能没有把 CDN 的 IP 段加入可信代理
	Samples []string `json:"samples"` // 被当作用户 IP 的地址示例（通常是 CDN 节点）
}

// Report 返回统计结果。
func (r *Resolver) Report() Report {
	idx := r.now().UnixNano() / int64(statWindow)
	s := &r.stats
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Report{Samples: []string{}}
	for _, b := range s.buckets {
		if b.idx == idx || b.idx == idx-1 {
			out.Total += b.total
			out.Suspect += b.suspect
		}
	}
	if out.Suspect > 0 {
		out.Samples = slices.Clone(s.samples)
		slices.Reverse(out.Samples)
	}
	out.Warning = out.Suspect >= warnMinSusp && float64(out.Suspect) >= warnMinRatio*float64(out.Total)
	return out
}
