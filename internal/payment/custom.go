package payment

import (
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// 原生产站点自行添加的支付类（EPay2、QrcodeEpay、EPaySubmitQrcode、TokenPay），按其源码实现。
func init() {
	Register("EPay2", ePay2{})
	Register("QrcodeEpay", ePayQrcode{})
	Register("EPaySubmitQrcode", ePaySubmitQrcode{})
	Register("TokenPay", tokenPay{})
}

// ---------- EPay2：EPay 的变体，支付类型取 payment_method，回调校验商户号 ----------

type ePay2 struct{}

func (ePay2) Form() Form {
	return Form{
		input("url", "URL", ""),
		input("pid", "PID", ""),
		input("key", "KEY", ""),
		input("payment_method", "Payment Method", "alipay or wxpay"),
	}
}

func (ePay2) Pay(e *Env, o *Order) (*Result, error) {
	p := map[string]any{
		"money":        phpDiv100(o.TotalAmount),
		"name":         o.TradeNo,
		"notify_url":   o.NotifyURL,
		"return_url":   o.ReturnURL,
		"out_trade_no": o.TradeNo,
		"pid":          e.Config["pid"],
		"type":         e.Config["payment_method"],
	}
	sign := md5hex(rawQuery(p) + e.Config.Str("key"))
	ps := append(flatten(p, ""), pair{"sign", sign}, pair{"sign_type", "MD5"})
	return &Result{Type: 1, Data: e.Config.Str("url") + "/submit.php?" + encodeQuery(ps)}, nil
}

func (ePay2) Notify(e *Env, n *Notification) (*Paid, error) {
	paid, err := ePay{}.Notify(e, n)
	if err != nil || n.Params.Str("pid") != e.Config.Str("pid") {
		return nil, errVerify
	}
	return paid, nil
}

// ---------- EPaySubmitQrcode：服务端请求 submit.php 收银台并从页面中解析二维码 ----------

type ePaySubmitQrcode struct{}

const (
	scrapeUA   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
	scrapeHops = 8
)

var (
	reScheme   = regexp.MustCompile(`(?i)^(alipays?://|weixin://|wxp://)`)
	reQRHost   = regexp.MustCompile(`(?i)^https?://(?:qr\.alipay\.com|render\.alipay\.com|qpay\.qq\.com|payapp\.weixin\.qq\.com|wx\.tenpay\.com)`)
	reAbsolute = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.\-]*://`)
	reQRValue  = regexp.MustCompile(`(?i)^(data:image/|alipays?://|weixin://|wxp://|https?://)`)
	reMoney    = regexp.MustCompile(`^\d+\.\d{2}$`)

	redirectPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:window\.)?location\.(?:replace|assign)\(\s*["']([^"']+)["']\s*\)`),
		regexp.MustCompile(`(?i)(?:window\.)?location(?:\.href)?\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?refresh["']?[^>]+content=["'][^"']*url=([^"'>\s]+)`),
	}
	qrPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(alipays?://[^\s"'<>\\]+)`),
		regexp.MustCompile(`(?i)(weixin://[^\s"'<>\\]+)`),
		regexp.MustCompile(`(?i)(wxp://[^\s"'<>\\]+)`),
		regexp.MustCompile(`(?i)(?:payurl|qrcode|code_url|codeurl|qr_?code)\s*[:=]\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)new\s+QRCode\([^,]+,\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)["'](data:image/[a-z]+;base64,[^"']+)["']`),
		regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']*(?:qrcode|qr[_-]?code|qrimg)[^"']*)["']`),
		regexp.MustCompile(`(?i)(https?://(?:qr\.alipay\.com|render\.alipay\.com|qpay\.qq\.com|payapp\.weixin\.qq\.com|wx\.tenpay\.com)/[^\s"'<>]+)`),
	}
)

func (ePaySubmitQrcode) Form() Form {
	return Form{
		input("url", "URL", "支付站点根地址，例如：https://pay.xxxxxx.com"),
		input("pid", "PID", "商户 ID"),
		input("key", "KEY", "商户密钥"),
		input("type", "TYPE", "强制支付类型：alipay 或 wxpay；留空按用户环境自动判断"),
	}
}

func submitURL(c Config) string {
	base := reMapiSuffix.ReplaceAllString(strings.TrimSpace(c.Str("url")), "")
	return strings.TrimRight(base, "/") + "/submit.php"
}

func submitPayType(c Config, ua string) string {
	t := strings.ToLower(strings.TrimSpace(c.Str("type")))
	if t == "alipay" || t == "wxpay" {
		return t
	}
	if strings.Contains(strings.ToLower(ua), "micromessenger") {
		return "wxpay"
	}
	return "alipay"
}

func (ePaySubmitQrcode) Pay(e *Env, o *Order) (*Result, error) {
	for _, k := range []string{"url", "pid", "key"} {
		if strings.TrimSpace(e.Config.Str(k)) == "" {
			return nil, gatewayError("EPay 配置不完整：" + k)
		}
	}
	endpoint := submitURL(e.Config)
	if !validURL(endpoint) {
		return nil, gatewayError("EPay URL 格式错误")
	}
	ps := []pair{
		{"pid", e.Config.Str("pid")},
		{"type", submitPayType(e.Config, o.UserAgent)},
		{"out_trade_no", o.TradeNo},
		{"notify_url", o.NotifyURL},
		{"return_url", o.ReturnURL},
		{"name", o.TradeNo},
		{"money", numberFormat2(o.TotalAmount)},
	}
	m := make(map[string]any, len(ps))
	for _, x := range ps {
		m[x.k] = x.v
	}
	ps = append(ps, pair{"sign", epaySign(m, e.Config.Str("key"))}, pair{"sign_type", "MD5"})

	qr := scrapeQR(e, endpoint+"?"+encodeQuery(ps))
	if qr == "" {
		return nil, gatewayError("未能从收银台页面解析到二维码，请查看系统日志后联系维护")
	}
	typ := 0
	if reScheme.MatchString(qr) && reEPayMobile.MatchString(o.UserAgent) {
		typ = 1
	}
	return &Result{Type: typ, Data: qr}, nil
}

// scrapeQR 跟随 HTTP / JS / meta 跳转（最多 8 跳，保持 Cookie），逐页尝试解析二维码。
func scrapeQR(e *Env, start string) string {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Transport: e.HTTP.Transport,
		Timeout:   e.HTTP.Timeout,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cur, last := start, ""
	for hop := 0; hop < scrapeHops; hop++ {
		req, err := http.NewRequestWithContext(e.Ctx, http.MethodGet, cur, nil)
		if err != nil {
			break
		}
		req.Header.Set("User-Agent", scrapeUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		resp, err := client.Do(req)
		if err != nil {
			zap.L().Warn("EPaySubmitQrcode 请求收银台失败", zap.Error(err))
			return ""
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		resp.Body.Close()
		body := string(b)
		last = body

		if qr := extractQR(body, cur); qr != "" {
			return qr
		}
		next := ""
		if loc := resp.Header.Get("Location"); loc != "" {
			next = absoluteURL(loc, cur)
		}
		if next == "" {
			if js := detectRedirect(body); js != "" {
				next = absoluteURL(js, cur)
			}
		}
		if next == "" {
			break
		}
		if reScheme.MatchString(next) || reQRHost.MatchString(next) {
			return next
		}
		cur = next
	}
	head := []rune(last)
	if len(head) > 3000 {
		head = head[:3000]
	}
	zap.L().Warn("EPaySubmitQrcode 未解析到二维码", zap.String("last_url", cur), zap.String("html_head", string(head)))
	return ""
}

func detectRedirect(body string) string {
	for _, re := range redirectPatterns {
		if m := re.FindStringSubmatch(body); m != nil {
			if u := html.UnescapeString(strings.TrimSpace(m[1])); u != "" {
				return u
			}
		}
	}
	return ""
}

func extractQR(body, base string) string {
	for _, re := range qrPatterns {
		m := re.FindStringSubmatch(body)
		if m == nil {
			continue
		}
		v := html.UnescapeString(strings.TrimSpace(m[1]))
		if v == "" {
			continue
		}
		if reQRValue.MatchString(v) {
			return v
		}
		return absoluteURL(v, base)
	}
	return ""
}

// absoluteURL 把页面中的相对地址补全为绝对地址（与原实现相同的简单规则）。
func absoluteURL(raw, base string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || reAbsolute.MatchString(raw) || strings.HasPrefix(raw, "data:") {
		return raw
	}
	b, err := url.Parse(base)
	if err != nil || b.Scheme == "" || b.Host == "" {
		return raw
	}
	origin := b.Scheme + "://" + b.Host
	if strings.HasPrefix(raw, "/") {
		return origin + raw
	}
	dir := "/"
	if i := strings.LastIndex(b.Path, "/"); i >= 0 {
		dir = b.Path[:i+1]
	}
	return origin + dir + raw
}

// Notify 在 EPayQrcode 校验的基础上要求回调金额与订单应付金额一致。原实现只与订单金额比较，
// 设置了手续费时永远不一致；这里通过 Paid.Amount 交由服务层与「订单金额 + 手续费」比较。
func (ePaySubmitQrcode) Notify(e *Env, n *Notification) (*Paid, error) {
	paid, err := ePayQrcode{}.Notify(e, n)
	if err != nil {
		return nil, err
	}
	money := n.Params.Str("money")
	if !reMoney.MatchString(money) {
		return nil, errVerify
	}
	cents, ok := parseCents(money)
	if !ok {
		return nil, errVerify
	}
	paid.Amount = &cents
	return paid, nil
}

func parseCents(s string) (int64, bool) {
	i := strings.IndexByte(s, '.')
	var v int64
	for _, c := range s[:i] + s[i+1:] {
		if v > (1<<62)/10 {
			return 0, false
		}
		v = v*10 + int64(c-'0')
	}
	return v, true
}

// ---------- TokenPay ----------

type tokenPay struct{}

func (tokenPay) Form() Form {
	return Form{
		input("token_pay_url", "API 地址", "您的 TokenPay API 接口地址(例如: https://token-pay.xxx.com)"),
		input("token_pay_apitoken", "API Token", "您的 TokenPay API Token"),
		input("token_pay_currency", "币种", "您的 TokenPay 币种，如 USDT_TRC20、TRX"),
	}
}

func (tokenPay) Pay(e *Env, o *Order) (*Result, error) {
	amount := phpDiv100(o.TotalAmount)
	p := map[string]any{
		"ActualAmount": amount,
		"OutOrderId":   o.TradeNo,
		"OrderUserKey": phpStr(o.UserID),
		"Currency":     e.Config["token_pay_currency"],
		"RedirectUrl":  o.ReturnURL,
		"NotifyUrl":    o.NotifyURL,
	}
	p["Signature"] = md5hex(rawQuery(p) + e.Config.Str("token_pay_apitoken"))
	p["ActualAmount"] = jsonAmount(amount)
	_, b, err := call(e, http.MethodPost, e.Config.Str("token_pay_url")+"/CreateOrder", marshalJSON(p),
		map[string]string{"Content-Type": "application/json", "User-Agent": "TokenPay"})
	res := decodeObject(b)
	if err != nil || res == nil || phpEmpty(res["success"]) {
		return nil, gatewayError("Failed to create order. Error: " + phpStr(res["message"]))
	}
	return &Result{Type: 1, Data: res["data"]}, nil
}

func (tokenPay) Notify(e *Env, n *Notification) (*Paid, error) {
	sign, ok := n.Params["Signature"].(string)
	if !ok || !hashEquals(md5hex(rawQuery(n.Params.without("Signature"))+e.Config.Str("token_pay_apitoken")), sign) {
		return nil, errVerify
	}
	if s, _ := phpNum(n.Params["Status"]); s != 1 {
		return nil, errVerify
	}
	return &Paid{TradeNo: n.Params.Str("OutOrderId"), CallbackNo: n.Params.Str("Id"), Reply: "ok"}, nil
}
