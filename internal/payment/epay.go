package payment

import (
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gvboard/internal/util"
)

func init() {
	Register("EPay", ePay{})
	Register("EPayQrcode", ePayQrcode{})
	// 原生产站点自行添加的 QrcodeEpay 与 EPayQrcode 相同，只保留旧名称供已有的支付方式使用
	registerAlias("QrcodeEpay", "EPayQrcode")
	Register("Paytaro", paytaro{})
	Register("PaytaroQR", paytaroQR{})
}

// ---------- EPay：跳转易支付收银台 /submit.php ----------

type ePay struct{}

func (ePay) Form() Form {
	return Form{
		input("url", "URL", ""),
		input("pid", "PID", ""),
		input("key", "KEY", ""),
		input("type", "TYPE", "支付类型，如: alipay, wxpay, qqpay"),
	}
}

func (ePay) Pay(e *Env, o *Order) (*Result, error) {
	p := map[string]any{
		"money":        phpDiv100(o.TotalAmount),
		"name":         o.TradeNo,
		"notify_url":   o.NotifyURL,
		"return_url":   o.ReturnURL,
		"out_trade_no": o.TradeNo,
		"pid":          e.Config["pid"],
	}
	if !phpEmpty(e.Config["type"]) {
		p["type"] = e.Config["type"]
	}
	sign := md5hex(rawQuery(p) + e.Config.Str("key"))
	ps := append(flatten(p, ""), pair{"sign", sign}, pair{"sign_type", "MD5"})
	return &Result{Type: 1, Data: e.Config.Str("url") + "/submit.php?" + encodeQuery(ps)}, nil
}

func (ePay) Notify(e *Env, n *Notification) (*Paid, error) {
	sign, ok := n.Params["sign"].(string)
	if !ok || !hashEquals(md5hex(rawQuery(n.Params.without("sign", "sign_type"))+e.Config.Str("key")), sign) {
		return nil, errVerify
	}
	if n.Params["trade_status"] != "TRADE_SUCCESS" {
		return nil, errVerify
	}
	return &Paid{TradeNo: n.Params.Str("out_trade_no"), CallbackNo: n.Params.Str("trade_no")}, nil
}

// epaySign 易支付文档签名：去掉 sign、sign_type 与空值，按键排序拼接后加密钥做 MD5（小写）。
func epaySign(p map[string]any, key string) string {
	keys := make([]string, 0, len(p))
	for k, v := range p {
		if k == "sign" || k == "sign_type" || v == nil || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + phpStr(p[k])
	}
	return md5hex(strings.Join(parts, "&") + key)
}

// ---------- EPayQrcode：通过 /mapi.php 下单，直接返回二维码 / 链接 ----------

type ePayQrcode struct{}

var reMapiSuffix = regexp.MustCompile(`(?i)/(?:submit|mapi)\.php(?:\?.*)?$`)

func (ePayQrcode) Form() Form {
	return Form{
		input("url", "URL", "支付站点根地址，例如：https://pay.xxxxxx.com"),
		input("pid", "PID", "商户 ID"),
		input("key", "KEY", "商户密钥"),
		input("type", "TYPE", "必填：alipay 或 wxpay；MAPI 不允许留空"),
	}
}

func mapiURL(c Config) string {
	base := reMapiSuffix.ReplaceAllString(strings.TrimSpace(c.Str("url")), "")
	return strings.TrimRight(base, "/") + "/mapi.php"
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func epayDevice(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "micromessenger"):
		return "wechat"
	case strings.Contains(l, "alipayclient"):
		return "alipay"
	case strings.Contains(l, "qq/"):
		return "qq"
	case reEPayMobile.MatchString(ua):
		return "mobile"
	}
	return "pc"
}

func (ePayQrcode) Pay(e *Env, o *Order) (*Result, error) {
	for _, k := range []string{"url", "pid", "key", "type"} {
		if strings.TrimSpace(e.Config.Str(k)) == "" {
			return nil, gatewayError("EPay 配置不完整：" + k)
		}
	}
	endpoint := mapiURL(e.Config)
	if !validURL(endpoint) {
		return nil, gatewayError("EPay URL 格式错误")
	}
	ip := o.ClientIP
	if ip == "" {
		ip = "127.0.0.1"
	}
	ps := []pair{
		{"pid", e.Config.Str("pid")},
		{"type", e.Config.Str("type")},
		{"out_trade_no", o.TradeNo},
		{"notify_url", o.NotifyURL},
		{"return_url", o.ReturnURL},
		{"name", o.TradeNo},
		{"money", numberFormat2(o.TotalAmount)},
		{"clientip", ip},
		{"device", epayDevice(o.UserAgent)},
	}
	m := make(map[string]any, len(ps))
	for _, x := range ps {
		m[x.k] = x.v
	}
	ps = append(ps, pair{"sign", epaySign(m, e.Config.Str("key"))}, pair{"sign_type", "MD5"})

	status, body, err := call(e, http.MethodPost, endpoint, []byte(encodeQuery(ps)), map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/x-www-form-urlencoded",
	})
	res := decodeObject(body)
	if err != nil || res == nil {
		return nil, gatewayError("支付接口网络异常或返回内容无法解析")
	}
	if code, _ := phpNum(res["code"]); status >= 400 || res["code"] == nil || int64(code) != 1 {
		msg := "API 下单失败"
		if res["msg"] != nil {
			msg = phpStr(res["msg"])
		}
		return nil, gatewayError(msg)
	}
	for _, f := range []string{"qrcode", "urlscheme", "payurl"} {
		if !phpEmpty(res[f]) {
			typ := 0
			if reEPayMobile.MatchString(o.UserAgent) {
				typ = 1
			}
			return &Result{Type: typ, Data: phpStr(res[f])}, nil
		}
	}
	return nil, gatewayError("接口下单成功，但没有返回 qrcode、urlscheme 或 payurl")
}

func (ePayQrcode) Notify(e *Env, n *Notification) (*Paid, error) {
	p := n.Params
	if phpEmpty(p["sign"]) || !hashEquals(epaySign(p, e.Config.Str("key")), strings.ToLower(p.Str("sign"))) {
		return nil, errVerify
	}
	if p.Str("pid") != e.Config.Str("pid") || p["trade_status"] != "TRADE_SUCCESS" {
		return nil, errVerify
	}
	if phpEmpty(p["out_trade_no"]) || phpEmpty(p["trade_no"]) {
		return nil, errVerify
	}
	return &Paid{TradeNo: p.Str("out_trade_no"), CallbackNo: p.Str("trade_no")}, nil
}

// ---------- Paytaro：收银台模式 ----------

const paytaroAPI = "https://v3.paytaro.com"

type paytaro struct{}

func paytaroForm() Form {
	return Form{
		input("pid", "App ID", "Paytaro 应用的 App ID；"),
		input("key", "App Secret", "Paytaro 应用的 App Secret；"),
	}
}

func (paytaro) Form() Form {
	return append(paytaroForm(), Field{Key: "alert1", Type: "alert",
		Content: `开户 / 开通支付方式请联系：<a href="https://t.me/smogate" target="_blank">@smogate</a>`})
}

func paytaroCred(c Config) (appID, secret string) {
	if !phpEmpty(c["pid"]) {
		appID = strings.TrimSpace(c.Str("pid"))
	}
	if !phpEmpty(c["key"]) {
		secret = strings.TrimSpace(c.Str("key"))
	}
	return
}

func (paytaro) Pay(e *Env, o *Order) (*Result, error) {
	appID, secret := paytaroCred(e.Config)
	all := []pair{
		{"pid", appID},
		{"type", "alipay"},
		{"out_trade_no", o.TradeNo},
		{"notify_url", o.NotifyURL},
		{"return_url", o.ReturnURL},
		{"name", o.TradeNo},
		{"money", numberFormat2(o.TotalAmount)},
	}
	var ps []pair
	m := map[string]any{}
	for _, x := range all {
		if x.v != "" {
			ps = append(ps, x)
			m[x.k] = x.v
		}
	}
	ps = append(ps, pair{"sign", epaySign(m, secret)}, pair{"sign_type", "MD5"})
	return &Result{Type: 1, Data: paytaroAPI + "/submit.php?" + encodeQuery(ps)}, nil
}

func (paytaro) Notify(e *Env, n *Notification) (*Paid, error) {
	_, secret := paytaroCred(e.Config)
	p := n.Params
	if secret == "" || phpEmpty(p["sign"]) || !hashEquals(epaySign(p, secret), strings.ToLower(p.Str("sign"))) {
		return nil, errVerify
	}
	if p["trade_status"] != "TRADE_SUCCESS" {
		return nil, errVerify
	}
	return &Paid{TradeNo: p.Str("out_trade_no"), CallbackNo: p.Str("trade_no"), Reply: "success"}, nil
}

// ---------- PaytaroQR：弹窗显码模式 ----------

// paytaroQRPage 加密货币支付页，由 Paytaro 安装脚本部署在面板域名下（Gvboard 不提供，需自行用 Web 服务器部署）。
const paytaroQRPage = "/paytaro-qr/pay.php"

type paytaroQR struct{}

func (paytaroQR) Form() Form {
	return append(paytaroForm(),
		input("method_uuid", "支付方式 UUID", "Paytaro 商户后台 → 应用管理 → 应用 → 付款方式 → 复制对应支付方式的 UUID（每个支付方式添加一条支付配置）"),
		Field{Key: "alert1", Type: "alert",
			Content: `弹窗显码模式：用户在本站页面内直接扫码 / 转账付款，不经过 Paytaro 收银台。开户与开通支付方式请联系 <a href="https://t.me/paytaro" target="_blank">@paytaro</a>`},
	)
}

func (paytaroQR) Pay(e *Env, o *Order) (*Result, error) {
	appID, secret := paytaroCred(e.Config)
	methodUUID := strings.TrimSpace(e.Config.Str("method_uuid"))
	if methodUUID == "" {
		return nil, gatewayError("Paytaro：请在支付配置中填写支付方式 UUID")
	}
	payload := map[string]any{
		"merchant_no":  o.TradeNo,
		"order_amount": jsonFloat(round2(o.TotalAmount)),
		"notify_url":   o.NotifyURL,
		"method_uuid":  methodUUID,
	}
	if o.ReturnURL != "" {
		payload["return_url"] = o.ReturnURL
	}
	status, body, err := call(e, http.MethodPost, paytaroAPI+"/v1/invoice/pay", marshalJSON(payload), map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-App-Secret": secret,
		"User-Agent":   "PaytaroQR-V2Board/" + appID,
	})
	if err != nil {
		return nil, gatewayError("Paytaro 网络异常，请稍后重试")
	}
	res := decodeObject(body)
	if res == nil {
		return nil, gatewayError("Paytaro 响应异常（HTTP " + strconv.Itoa(status) + "）")
	}
	if status < 200 || status >= 300 {
		msg := httpStatusText(status)
		if res["error"] != nil {
			msg = phpStr(res["error"])
		} else if res["message"] != nil {
			msg = phpStr(res["message"])
		}
		return nil, gatewayError("Paytaro：" + msg)
	}
	payment, _ := res["payment"].(map[string]any)
	if payment == nil || phpEmpty(payment["data"]) || phpEmpty(res["uuid"]) {
		return nil, gatewayError("Paytaro 返回数据不完整")
	}

	linkType := strings.ToLower(phpStr(payment["link_type"]))
	if linkType == "h5" || linkType == "pc" || strings.ToLower(phpStr(payment["type"])) == "alipay" {
		if rePaytaroMobile.MatchString(o.UserAgent) {
			if !phpEmpty(payment["mobile_url"]) {
				return &Result{Type: 1, Data: payment["mobile_url"]}, nil
			}
			return &Result{Type: 1, Data: payment["data"]}, nil
		}
		if linkType == "pc" {
			return &Result{Type: 1, Data: payment["data"]}, nil
		}
		return &Result{Type: 0, Data: payment["data"]}, nil
	}
	return &Result{Type: 1, Data: siteBase(o.NotifyURL) + paytaroQRPage + "?uuid=" + util.RawURLEncode(phpStr(res["uuid"]))}, nil
}

func siteBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (paytaroQR) Notify(e *Env, n *Notification) (*Paid, error) {
	_, secret := paytaroCred(e.Config)
	got := n.Header.Get("X-App-Secret")
	if got == "" || !hashEquals(secret, got) {
		return nil, errVerify
	}
	p := n.Params
	if s := p["status"]; s != "PAID" && s != "SUCCESS" {
		return nil, errVerify
	}
	tradeNo, callbackNo := p.Str("merchant_no"), p.Str("callback_no")
	if callbackNo == "" {
		callbackNo = p.Str("transaction_no")
	}
	if tradeNo == "" || callbackNo == "" {
		return nil, errVerify
	}
	return &Paid{TradeNo: tradeNo, CallbackNo: callbackNo, Reply: "success"}, nil
}
