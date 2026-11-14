package payment

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func init() {
	Register("BEasyPaymentUSDT", bepusdt{})
	Register("Epusdt", epusdt{})
}

// ---------- BEasyPaymentUSDT ----------

type bepusdt struct{}

func (bepusdt) Form() Form {
	return Form{
		input("bepusdt_url", "API 地址", "您的 BEPUSDT API 接口地址(例如: https://xxx.com)"),
		input("bepusdt_apitoken", "API Token", "您的 BEPUSDT API Token"),
		input("bepusdt_trade_type", "交易类型", "您的 BEPUSDT 交易类型"),
	}
}

func (bepusdt) Pay(e *Env, o *Order) (*Result, error) {
	amount := phpDiv100(o.TotalAmount)
	p := map[string]any{
		"amount":       amount,
		"trade_type":   e.Config["bepusdt_trade_type"],
		"notify_url":   o.NotifyURL,
		"order_id":     o.TradeNo,
		"redirect_url": o.ReturnURL,
	}
	p["signature"] = md5hex(rawQuery(p) + e.Config.Str("bepusdt_apitoken"))
	p["amount"] = jsonAmount(amount)
	_, body, err := call(e, http.MethodPost, e.Config.Str("bepusdt_url")+"/api/v1/order/create-transaction",
		marshalJSON(p), map[string]string{"Content-Type": "application/json", "User-Agent": "BEPUSDT"})
	res := decodeObject(body)
	if err != nil || res == nil {
		return nil, gatewayError("Failed to create order. Error: ")
	}
	if code, ok := phpNum(res["status_code"]); !ok || code != 200 {
		return nil, gatewayError("Failed to create order. Error: " + phpStr(res["message"]))
	}
	return &Result{Type: 1, Data: dig(res, "data", "payment_url")}, nil
}

func (bepusdt) Notify(e *Env, n *Notification) (*Paid, error) {
	sign, ok := n.Params["signature"].(string)
	gen := md5hex(rawQuery(n.Params.without("signature")) + e.Config.Str("bepusdt_apitoken"))
	if !ok || !hashEquals(gen, sign) {
		return nil, errVerify
	}
	if s, _ := phpNum(n.Params["status"]); s != 2 {
		return nil, errVerify
	}
	return &Paid{TradeNo: n.Params.Str("order_id"), CallbackNo: n.Params.Str("trade_id"), Reply: "ok"}, nil
}

// ---------- Epusdt（GMPay） ----------

type epusdt struct{}

func (epusdt) Form() Form {
	return Form{
		input("epusdt_url", "API 地址", "Epusdt API 接口地址(例如: https://xxx.com)"),
		input("epusdt_pid", "PID", "Epusdt 后台的 pid"),
		input("epusdt_token", "Token", "Epusdt 后台的 secret_key"),
		input("epusdt_currency", "法币", "默认 cny"),
		input("epusdt_asset", "代币", "默认 usdt"),
		input("epusdt_network", "网络", "留空时进入 GMPay 选择链路界面，填写时按该网络直接发起订单"),
	}
}

// epusdtSign 键排序，跳过 signature 与空值，数字按 %.12F 去掉末尾 0 后拼接，加密钥做 MD5（小写）。
func epusdtSign(p map[string]any, token string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := p[k]
		if k == "signature" || v == nil || v == "" {
			continue
		}
		var s string
		switch x := v.(type) {
		case json.Number:
			f, _ := x.Float64()
			s = fixed12(f)
		case float64:
			s = fixed12(x)
		case jsonFloat:
			s = fixed12(float64(x))
		case int64:
			s = fixed12(float64(x))
		default:
			s = phpStr(v)
		}
		parts = append(parts, k+"="+s)
	}
	return md5hex(strings.Join(parts, "&") + token)
}

func fixed12(f float64) string {
	s := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 12, 64), "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func lowerOr(c Config, key, def string) string {
	if phpEmpty(c[key]) {
		return def
	}
	return strings.ToLower(strings.TrimSpace(c.Str(key)))
}

func (epusdt) Pay(e *Env, o *Order) (*Result, error) {
	network := strings.ToLower(strings.TrimSpace(e.Config.Str("epusdt_network")))
	token := lowerOr(e.Config, "epusdt_asset", "usdt")
	chain := network
	if chain == "" {
		chain = "tron"
	}
	p := map[string]any{
		"pid":          strings.TrimSpace(e.Config.Str("epusdt_pid")),
		"order_id":     o.TradeNo,
		"currency":     lowerOr(e.Config, "epusdt_currency", "cny"),
		"token":        token,
		"network":      chain,
		"amount":       jsonFloat(round2(o.TotalAmount)),
		"notify_url":   o.NotifyURL,
		"redirect_url": o.ReturnURL,
	}
	p["signature"] = epusdtSign(p, strings.TrimSpace(e.Config.Str("epusdt_token")))
	base := strings.TrimRight(e.Config.Str("epusdt_url"), "/")
	header := map[string]string{"Content-Type": "application/json", "User-Agent": "epusdt"}

	_, body, err := call(e, http.MethodPost, base+"/payments/gmpay/v1/order/create-transaction", marshalJSON(p), header)
	res := decodeObject(body)
	if err != nil || !statusOK(res) {
		return nil, gatewayError(messageOr(res, "epusdt create order failed"))
	}
	payURL := dig(res, "data", "payment_url")
	if network != "" {
		tradeID := dig(res, "data", "trade_id")
		if tradeID == nil || tradeID == "" {
			return nil, gatewayError("epusdt create order response missing trade_id")
		}
		sw := map[string]any{"trade_id": phpStr(tradeID), "token": token, "network": network}
		_, body, err := call(e, http.MethodPost, base+"/pay/switch-network", marshalJSON(sw), header)
		res := decodeObject(body)
		if err != nil || !statusOK(res) {
			return nil, gatewayError(messageOr(res, "epusdt switch network failed"))
		}
		if v := dig(res, "data", "payment_url"); v != nil {
			payURL = v
		}
	}
	if phpEmpty(payURL) {
		return nil, gatewayError("epusdt payment url missing")
	}
	return &Result{Type: 1, Data: payURL}, nil
}

func statusOK(res map[string]any) bool {
	code, ok := phpNum(res["status_code"])
	return res != nil && ok && int64(code) == 200
}

func messageOr(res map[string]any, def string) string {
	if res != nil && res["message"] != nil {
		return phpStr(res["message"])
	}
	return def
}

func (epusdt) Notify(e *Env, n *Notification) (*Paid, error) {
	token := strings.TrimSpace(e.Config.Str("epusdt_token"))
	if token == "" || !n.Params.Has("signature") {
		return nil, errVerify
	}
	sign := strings.ToLower(n.Params.Str("signature"))
	if !hashEquals(epusdtSign(n.Params.without("signature"), token), sign) {
		return nil, errVerify
	}
	if s, ok := phpNum(n.Params["status"]); !ok || int64(s) != 2 {
		return nil, errVerify
	}
	return &Paid{TradeNo: n.Params.Str("order_id"), CallbackNo: n.Params.Str("trade_id"), Reply: "ok"}, nil
}
