package payment

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

func init() {
	Register("StripeALL", stripeAll{})
	Register("StripeAlipay", stripeIntent{method: "alipay", typ: 1})
	Register("StripeWepay", stripeIntent{method: "wechat_pay", typ: 0})
	Register("StripeCheckout", stripeCheckout{})
	Register("StripeCredit", stripeCredit{})
}

const (
	stripeAPI       = "https://api.stripe.com/v1"
	stripeTolerance = 300 * time.Second

	msgGatewayFailed  = "Payment gateway request failed"
	msgExchangeFailed = "Currency conversion has timed out, please try again later"
	msgCardFailed     = "Payment failed. Please check your credit card information"
)

var exchangeAPIs = []func(to string) (string, []string){
	func(to string) (string, []string) {
		return "https://api.exchangerate-api.com/v4/latest/CNY", []string{"rates", to}
	},
	func(to string) (string, []string) {
		return "https://api.frankfurter.app/latest?from=CNY&to=" + url.QueryEscape(to), []string{"rates", to}
	},
}

// exchange 人民币兑目标货币汇率：依次尝试两个汇率接口（原 StripeALL 的做法，所有 Stripe 网关统一使用）。
func exchange(e *Env, currency string) (float64, error) {
	to := strings.ToUpper(currency)
	if to == "CNY" {
		return 1, nil
	}
	for _, api := range exchangeAPIs {
		u, path := api(to)
		_, b, err := call(e, http.MethodGet, u, nil, nil)
		if err != nil {
			continue
		}
		if r, ok := phpNum(dig(decodeObject(b), path...)); ok && r > 0 {
			return r, nil
		}
	}
	return 0, gatewayError(msgExchangeFailed)
}

// stripeAmount 订单金额（分）按汇率换算为目标货币的最小单位，向下取整。
func stripeAmount(e *Env, total int64) (string, error) {
	rate, err := exchange(e, e.Config.Str("currency"))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(int64(math.Floor(float64(total)*rate)), 10), nil
}

// stripePost 调用 Stripe API（表单编码），返回解析后的对象；失败时返回 Stripe 的错误信息。
func stripePost(e *Env, path string, form url.Values) (map[string]any, error) {
	status, b, err := call(e, http.MethodPost, stripeAPI+path, []byte(form.Encode()), map[string]string{
		"Authorization": "Bearer " + e.Config.Str("stripe_sk_live"),
		"Content-Type":  "application/x-www-form-urlencoded",
	})
	if err != nil {
		return nil, err
	}
	res := decodeObject(b)
	if status >= 300 || res == nil {
		msg := digStr(res, "error", "message")
		if msg == "" {
			msg = httpStatusText(status)
		}
		return nil, errors.New(msg)
	}
	return res, nil
}

func stripeFailed(e *Env, path string, err error) error {
	zap.L().Warn("Stripe 请求失败", zap.String("path", path), zap.Error(err))
	return gatewayError(msgGatewayFailed)
}

// stripeEvent 校验 Stripe-Signature（t=时间戳,v1=签名）并解析事件。
func stripeEvent(e *Env, n *Notification) (map[string]any, error) {
	var ts string
	var sigs []string
	for _, part := range strings.Split(n.Header.Get("Stripe-Signature"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return nil, errVerify
	}
	if d := e.Now.Sub(time.Unix(t, 0)); d > stripeTolerance || d < -stripeTolerance {
		return nil, errVerify
	}
	want := hmacSHA256([]byte(e.Config.Str("stripe_webhook_key")), []byte(ts+"."+string(n.Body)))
	for _, s := range sigs {
		if hashEquals(want, s) {
			if ev := decodeObject(n.Body); ev != nil {
				return ev, nil
			}
		}
	}
	return nil, errVerify
}

func stripeObject(ev map[string]any) map[string]any {
	o, _ := dig(ev, "data", "object").(map[string]any)
	return o
}

// stripeCommonNotify 处理 PaymentIntent、Checkout Session 与 Charge 的成功事件。
func stripeCommonNotify(ev map[string]any, intent, session, charge bool) (*Paid, error) {
	obj := stripeObject(ev)
	switch ev["type"] {
	case "payment_intent.succeeded":
		if intent && obj["status"] == "succeeded" && dig(obj, "metadata", "out_trade_no") != nil {
			return &Paid{TradeNo: digStr(obj, "metadata", "out_trade_no"), CallbackNo: phpStr(obj["id"])}, nil
		}
	case "checkout.session.completed":
		if session && obj["payment_status"] == "paid" {
			return &Paid{TradeNo: phpStr(obj["client_reference_id"]), CallbackNo: phpStr(obj["payment_intent"])}, nil
		}
	case "checkout.session.async_payment_succeeded":
		if session {
			return &Paid{TradeNo: phpStr(obj["client_reference_id"]), CallbackNo: phpStr(obj["payment_intent"])}, nil
		}
	case "charge.succeeded":
		if charge && obj["status"] == "succeeded" {
			no := dig(obj, "metadata", "out_trade_no")
			if no == nil {
				no = dig(obj, "source", "metadata", "out_trade_no")
			}
			if no != nil {
				return &Paid{TradeNo: phpStr(no), CallbackNo: phpStr(obj["id"])}, nil
			}
		}
	}
	return nil, errVerify
}

func stripeKeyForm(pk bool) Form {
	f := Form{
		input("currency", "货币单位", ""),
		input("stripe_sk_live", "SK_LIVE", ""),
	}
	if pk {
		f = append(f, input("stripe_pk_live", "PK_LIVE", ""))
	}
	return append(f, input("stripe_webhook_key", "WebHook密钥签名", ""))
}

func stripeDescriptor(o *Order) string {
	no := o.TradeNo
	if len(no) > 8 {
		no = no[len(no)-8:]
	}
	return "user-#" + strconv.FormatInt(o.UserID, 10) + "-" + no
}

// createIntent 创建指定类型的支付方式并确认 PaymentIntent，返回 next_action。
func createIntent(e *Env, o *Order, method string, meta map[string]string) (map[string]any, error) {
	amount, err := stripeAmount(e, o.TotalAmount)
	if err != nil {
		return nil, err
	}
	pm, err := stripePost(e, "/payment_methods", url.Values{"type": {method}})
	if err != nil {
		return nil, stripeFailed(e, "/payment_methods", err)
	}
	f := url.Values{
		"amount":                             {amount},
		"currency":                           {e.Config.Str("currency")},
		"confirm":                            {"true"},
		"payment_method":                     {phpStr(pm["id"])},
		"automatic_payment_methods[enabled]": {"true"},
		"statement_descriptor":               {stripeDescriptor(o)},
		"return_url":                         {o.ReturnURL},
	}
	for k, v := range meta {
		f.Set("metadata["+k+"]", v)
	}
	if method == "wechat_pay" {
		f.Set("payment_method_options[wechat_pay][client]", "web")
	}
	pi, err := stripePost(e, "/payment_intents", f)
	if err != nil {
		return nil, stripeFailed(e, "/payment_intents", err)
	}
	next, _ := pi["next_action"].(map[string]any)
	if next == nil {
		return nil, gatewayError(msgGatewayFailed)
	}
	return next, nil
}

// intentResult 从 next_action 取支付宝跳转链接或微信二维码内容。
func intentResult(method string, next map[string]any) (*Result, error) {
	switch method {
	case "alipay":
		if u := dig(next, "alipay_handle_redirect", "url"); u != nil {
			return &Result{Type: 1, Data: u}, nil
		}
		return nil, gatewayError("unable get Alipay redirect url")
	case "wechat_pay":
		if d := dig(next, "wechat_pay_display_qr_code", "data"); d != nil {
			return &Result{Type: 0, Data: d}, nil
		}
		return nil, gatewayError("unable get WeChat Pay redirect url")
	}
	return &Result{Type: 0, Data: nil}, nil
}

// ---------- StripeALL：alipay / wechat_pay 走 PaymentIntent，cards 走 Checkout ----------

type stripeAll struct{}

func (stripeAll) Form() Form {
	return Form{
		input("currency", "货币单位", "请使用符合ISO 4217标准的三位字母，例如GBP"),
		input("stripe_sk_live", "SK_LIVE", ""),
		input("stripe_webhook_key", "WebHook密钥签名", "whsec_...."),
		input("payment_method", "支付方式", "请输入alipay, wechat_pay, cards"),
	}
}

func (stripeAll) Pay(e *Env, o *Order) (*Result, error) {
	method := e.Config.Str("payment_method")
	if method != "cards" {
		next, err := createIntent(e, o, method, map[string]string{
			"user_id":        strconv.FormatInt(o.UserID, 10),
			"customer_email": o.Email,
			"out_trade_no":   o.TradeNo,
		})
		if err != nil {
			return nil, err
		}
		return intentResult(method, next)
	}
	amount, err := stripeAmount(e, o.TotalAmount)
	if err != nil {
		return nil, err
	}
	f := url.Values{
		"success_url":                                   {o.ReturnURL},
		"client_reference_id":                           {o.TradeNo},
		"payment_method_types[0]":                       {"card"},
		"line_items[0][price_data][currency]":           {e.Config.Str("currency")},
		"line_items[0][price_data][unit_amount]":        {amount},
		"line_items[0][price_data][product_data][name]": {stripeDescriptor(o)},
		"line_items[0][quantity]":                       {"1"},
		"mode":                                          {"payment"},
		"invoice_creation[enabled]":                     {"true"},
		"phone_number_collection[enabled]":              {"false"},
	}
	if o.Email != "" {
		f.Set("customer_email", o.Email)
	}
	s, err := stripePost(e, "/checkout/sessions", f)
	if err != nil {
		return nil, stripeFailed(e, "/checkout/sessions", err)
	}
	return &Result{Type: 1, Data: s["url"]}, nil
}

func (stripeAll) Notify(e *Env, n *Notification) (*Paid, error) {
	ev, err := stripeEvent(e, n)
	if err != nil {
		return nil, err
	}
	return stripeCommonNotify(ev, true, true, false)
}

// ---------- StripeAlipay / StripeWepay：原 Sources API 已下线，改用 PaymentIntent（决策 D26） ----------

type stripeIntent struct {
	method string
	typ    int
}

func (stripeIntent) Form() Form { return stripeKeyForm(false) }

func (g stripeIntent) Pay(e *Env, o *Order) (*Result, error) {
	next, err := createIntent(e, o, g.method, map[string]string{
		"user_id":      strconv.FormatInt(o.UserID, 10),
		"out_trade_no": o.TradeNo,
		"identifier":   "",
	})
	if err != nil {
		return nil, err
	}
	return intentResult(g.method, next)
}

func (stripeIntent) Notify(e *Env, n *Notification) (*Paid, error) {
	ev, err := stripeEvent(e, n)
	if err != nil {
		return nil, err
	}
	return stripeCommonNotify(ev, true, false, true)
}

// ---------- StripeCheckout ----------

type stripeCheckout struct{}

func (stripeCheckout) Form() Form {
	return Form{
		input("currency", "货币单位", ""),
		input("stripe_sk_live", "SK_LIVE", "API 密钥"),
		input("stripe_pk_live", "PK_LIVE", "API 公钥"),
		input("stripe_webhook_key", "WebHook 密钥签名", ""),
		input("stripe_custom_field_name", "自定义字段名称", "例如可设置为“联系方式”，以便及时与客户取得联系"),
	}
}

func (stripeCheckout) Pay(e *Env, o *Order) (*Result, error) {
	amount, err := stripeAmount(e, o.TotalAmount)
	if err != nil {
		return nil, err
	}
	field := "Contact Infomation"
	if e.Config.Has("stripe_custom_field_name") {
		field = e.Config.Str("stripe_custom_field_name")
	}
	f := url.Values{
		"success_url":                         {o.ReturnURL},
		"cancel_url":                          {o.ReturnURL},
		"client_reference_id":                 {o.TradeNo},
		"line_items[0][price_data][currency]": {e.Config.Str("currency")},
		"line_items[0][price_data][product_data][name]": {o.TradeNo},
		"line_items[0][price_data][unit_amount]":        {amount},
		"line_items[0][quantity]":                       {"1"},
		"mode":                                          {"payment"},
		"invoice_creation[enabled]":                     {"true"},
		"phone_number_collection[enabled]":              {"true"},
		"custom_fields[0][key]":                         {"contactinfo"},
		"custom_fields[0][label][type]":                 {"custom"},
		"custom_fields[0][label][custom]":               {field},
		"custom_fields[0][type]":                        {"text"},
	}
	s, err := stripePost(e, "/checkout/sessions", f)
	if err != nil {
		return nil, gatewayError("Failed to create order. Error: " + err.Error())
	}
	return &Result{Type: 1, Data: s["url"]}, nil
}

func (stripeCheckout) Notify(e *Env, n *Notification) (*Paid, error) {
	ev, err := stripeEvent(e, n)
	if err != nil {
		return nil, err
	}
	return stripeCommonNotify(ev, false, true, false)
}

// ---------- StripeCredit：前端用公钥生成 token 后直接扣款 ----------

type stripeCredit struct{}

func (stripeCredit) Form() Form { return stripeKeyForm(true) }

func (stripeCredit) Pay(e *Env, o *Order) (*Result, error) {
	amount, err := stripeAmount(e, o.TotalAmount)
	if err != nil {
		return nil, err
	}
	ch, err := stripePost(e, "/charges", url.Values{
		"amount":                 {amount},
		"currency":               {e.Config.Str("currency")},
		"source":                 {o.StripeToken},
		"metadata[user_id]":      {strconv.FormatInt(o.UserID, 10)},
		"metadata[out_trade_no]": {o.TradeNo},
		"metadata[identifier]":   {""},
	})
	if err != nil || ch["paid"] != true {
		return nil, gatewayError(msgCardFailed)
	}
	return &Result{Type: 2, Data: true}, nil
}

func (stripeCredit) Notify(e *Env, n *Notification) (*Paid, error) {
	ev, err := stripeEvent(e, n)
	if err != nil {
		return nil, err
	}
	return stripeCommonNotify(ev, false, false, true)
}
