package payment

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func init() {
	Register("BTCPay", btcPay{})
	Register("Coinbase", coinbase{})
	Register("CoinPayments", coinPayments{})
}

func hmacSHA256(key, msg []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

func hmacSHA512(key, msg []byte) string {
	m := hmac.New(sha512.New, key)
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// webhookPayload 原项目取 trim(请求体)，请求体为空时取 json_encode($_POST)（此时验签必然失败）。
func webhookPayload(n *Notification) []byte {
	return bytes.Trim(n.Body, " \t\n\r\x00\x0B")
}

// ---------- BTCPay ----------

type btcPay struct{}

func (btcPay) Form() Form {
	return Form{
		input("btcpay_url", "API接口所在网址(包含最后的斜杠)", ""),
		input("btcpay_storeId", "storeId", ""),
		input("btcpay_api_key", "API KEY", "个人设置中的API KEY(非商店设置中的)"),
		input("btcpay_webhook_key", "WEBHOOK KEY", ""),
	}
}

func (btcPay) invoicesURL(c Config) string {
	return c.Str("btcpay_url") + "api/v1/stores/" + c.Str("btcpay_storeId") + "/invoices"
}

func (g btcPay) Pay(e *Env, o *Order) (*Result, error) {
	body := marshalJSON(map[string]any{
		"jsonResponse": true,
		"amount":       numberFormat2(o.TotalAmount),
		"currency":     "CNY",
		"metadata":     map[string]any{"orderId": o.TradeNo},
	})
	_, b, err := call(e, http.MethodPost, g.invoicesURL(e.Config), body, map[string]string{
		"Authorization": "token " + e.Config.Str("btcpay_api_key"),
		"Content-Type":  "application/json",
	})
	res := decodeObject(b)
	if err != nil || phpEmpty(res["checkoutLink"]) {
		return nil, gatewayError("error!")
	}
	return &Result{Type: 1, Data: res["checkoutLink"]}, nil
}

// Notify 验证 Btcpay-Sig 后查询发票详情取订单号。原项目不检查发票状态，任何 Webhook 事件
// （包括发票创建）都会使订单开通；这里要求发票状态为 Settled。
func (g btcPay) Notify(e *Env, n *Notification) (*Paid, error) {
	payload := webhookPayload(n)
	want := "sha256=" + hmacSHA256([]byte(e.Config.Str("btcpay_webhook_key")), payload)
	if !hashEquals(n.Header.Get("Btcpay-Sig"), want) {
		return nil, errVerify
	}
	invoiceID := digStr(decodeObject(payload), "invoiceId")
	if invoiceID == "" {
		return nil, errVerify
	}
	_, b, err := call(e, http.MethodGet, g.invoicesURL(e.Config)+"/"+url.PathEscape(invoiceID), nil, map[string]string{
		"Authorization": "token " + e.Config.Str("btcpay_api_key"),
	})
	detail := decodeObject(b)
	if err != nil || detail == nil || detail["status"] != "Settled" {
		return nil, errVerify
	}
	return &Paid{TradeNo: digStr(detail, "metadata", "orderId"), CallbackNo: invoiceID}, nil
}

// ---------- Coinbase Commerce ----------

type coinbase struct{}

func (coinbase) Form() Form {
	return Form{
		input("coinbase_url", "接口地址", ""),
		input("coinbase_api_key", "API KEY", ""),
		input("coinbase_webhook_key", "WEBHOOK KEY", ""),
	}
}

func (coinbase) Pay(e *Env, o *Order) (*Result, error) {
	body := encodeQuery([]pair{
		{"name", "订阅套餐"},
		{"description", "订单号 " + o.TradeNo},
		{"pricing_type", "fixed_price"},
		{"local_price[amount]", numberFormat2(o.TotalAmount)},
		{"local_price[currency]", "CNY"},
		{"metadata[outTradeNo]", o.TradeNo},
	})
	_, b, err := call(e, http.MethodPost, e.Config.Str("coinbase_url"), []byte(body), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"X-CC-Api-Key": e.Config.Str("coinbase_api_key"),
		"X-CC-Version": "2018-03-22",
	})
	res := decodeObject(b)
	if err != nil || phpEmpty(dig(res, "data", "hosted_url")) {
		return nil, gatewayError("error!")
	}
	return &Result{Type: 1, Data: dig(res, "data", "hosted_url")}, nil
}

// Notify 原项目不检查事件类型，charge:created 等事件也会使订单开通；这里只接受已确认的付款事件。
func (coinbase) Notify(e *Env, n *Notification) (*Paid, error) {
	payload := webhookPayload(n)
	want := hmacSHA256([]byte(e.Config.Str("coinbase_webhook_key")), payload)
	if !hashEquals(n.Header.Get("X-Cc-Webhook-Signature"), want) {
		return nil, errVerify
	}
	ev := decodeObject(payload)
	if t := digStr(ev, "event", "type"); t != "charge:confirmed" && t != "charge:resolved" {
		return nil, errVerify
	}
	return &Paid{TradeNo: digStr(ev, "event", "data", "metadata", "outTradeNo"), CallbackNo: digStr(ev, "event", "id")}, nil
}

// ---------- CoinPayments ----------

type coinPayments struct{}

func (coinPayments) Form() Form {
	return Form{
		input("coinpayments_merchant_id", "Merchant ID", "商户 ID，填写您在 Account Settings 中得到的 ID"),
		input("coinpayments_ipn_secret", "IPN Secret", "通知密钥，填写您在 Merchant Settings 中自行设置的值"),
		input("coinpayments_currency", "货币代码", "填写您的货币代码（大写），建议与 Merchant Settings 中的值相同"),
	}
}

func (coinPayments) Pay(e *Env, o *Order) (*Result, error) {
	var success string
	if u, err := url.Parse(o.ReturnURL); err == nil {
		success = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	}
	q := encodeQuery([]pair{
		{"cmd", "_pay_simple"},
		{"reset", "1"},
		{"merchant", e.Config.Str("coinpayments_merchant_id")},
		{"item_name", o.TradeNo},
		{"item_number", o.TradeNo},
		{"want_shipping", "0"},
		{"currency", e.Config.Str("coinpayments_currency")},
		{"amountf", numberFormat2(o.TotalAmount)},
		{"success_url", success},
		{"cancel_url", o.ReturnURL},
		{"ipn_url", o.NotifyURL},
	})
	return &Result{Type: 1, Data: "https://www.coinpayments.net/index.php?" + q}, nil
}

// Notify HMAC 先按 CoinPayments 文档对原始请求体校验，再按原项目的方式（参数排序后重新编码）校验，任一通过即可。
func (coinPayments) Notify(e *Env, n *Notification) (*Paid, error) {
	p := n.Params
	if !p.Has("merchant") || p.Str("merchant") != strings.TrimSpace(e.Config.Str("coinpayments_merchant_id")) {
		return nil, errVerify
	}
	key := []byte(strings.TrimSpace(e.Config.Str("coinpayments_ipn_secret")))
	got := n.Header.Get("Hmac")
	if !hashEquals(hmacSHA512(key, n.Body), got) &&
		!hashEquals(hmacSHA512(key, []byte(stripslashes(encodeQuery(flatten(p, ""))))), got) {
		return nil, errVerify
	}
	status, _ := phpNum(p["status"])
	if status < 100 && status != 2 {
		return nil, errVerify
	}
	return &Paid{TradeNo: p.Str("item_number"), CallbackNo: p.Str("txn_id"), Reply: "IPN OK"}, nil
}
