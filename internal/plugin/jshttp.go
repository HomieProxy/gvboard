package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// JS 插件 HTTP 请求限制。
const (
	httpDefaultTimeout = 10 * time.Second
	httpMaxTimeout     = 30 * time.Second
	httpMaxRequest     = 1 << 20
	httpMaxResponse    = 2 << 20
	httpMaxRedirects   = 3
)

var errBlockedAddr = errors.New("不允许访问内网或本机地址")

// blockedPrefixes 除 IsPrivate / IsLoopback 等之外需要拦截的地址段。
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // 运营商级 NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64，可映射到内网 IPv4
	netip.MustParsePrefix("2002::/16"),    // 6to4
}

// blockedIP 内网、本机、链路本地、组播等不允许 JS 插件访问的地址。
func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// jsHTTP 一个 JS 插件的 HTTP 客户端。
type jsHTTP struct {
	domains []domain
	client  *http.Client
}

func newJSHTTP(domains []domain, allowPrivate bool) *jsHTTP {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		// 在连接实际解析出的地址前检查，防止 DNS 重绑定绕过
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || blockedIP(ip) {
				return errBlockedAddr
			}
			return nil
		}
	}
	h := &jsHTTP{domains: domains}
	h.client = &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: httpMaxTimeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= httpMaxRedirects {
				return fmt.Errorf("重定向次数超过 %d 次", httpMaxRedirects)
			}
			return h.allowed(req.URL)
		},
	}
	return h
}

func (h *jsHTTP) allowed(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http / https 地址")
	}
	if u.User != nil {
		return fmt.Errorf("地址中不能包含用户名密码")
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range h.domains {
		if d.match(host, u.Port(), u.Scheme) {
			return nil
		}
	}
	return fmt.Errorf("插件未声明可访问域名 %s", u.Host)
}

type httpRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
	Timeout time.Duration
}

type httpResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func (h *jsHTTP) do(ctx context.Context, r httpRequest) (*httpResponse, error) {
	u, err := url.Parse(r.URL)
	if err != nil {
		return nil, fmt.Errorf("地址无效")
	}
	if err := h.allowed(u); err != nil {
		return nil, err
	}
	if len(r.Body) > httpMaxRequest {
		return nil, fmt.Errorf("请求内容不能超过 %d MB", httpMaxRequest>>20)
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return nil, fmt.Errorf("不支持的请求方法 %s", r.Method)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = httpDefaultTimeout
	}
	timeout = min(timeout, httpMaxTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range r.Headers {
		if strings.EqualFold(k, "host") {
			continue
		}
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("请求失败：%v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%v", err)
	}
	if len(data) > httpMaxResponse {
		return nil, fmt.Errorf("响应内容超过 %d MB", httpMaxResponse>>20)
	}
	out := &httpResponse{Status: resp.StatusCode, Headers: map[string]string{}, Body: string(data)}
	for k, v := range resp.Header {
		if len(v) > 0 {
			out.Headers[strings.ToLower(k)] = v[0]
		}
	}
	return out, nil
}
