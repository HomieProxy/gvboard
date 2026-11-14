package payment

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gvboard/internal/httpx"
)

const mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148"

func gwMsg(err error) string {
	var e *httpx.Error
	if errors.As(err, &e) {
		return e.Msg
	}
	return ""
}

func TestRegistry(t *testing.T) {
	names := Names()
	want := []string{"PaytaroQR", "Paytaro", "AlipayF2F", "BEasyPaymentUSDT", "BTCPay", "CoinPayments", "Coinbase",
		"EPay", "EPay2", "EPayQrcode", "EPaySubmitQrcode", "Epusdt", "QrcodeEpay",
		"StripeALL", "StripeAlipay", "StripeCheckout", "StripeCredit", "StripeWepay", "TokenPay", "WechatPayNative"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("网关列表顺序与原项目不一致:\n%v", names)
	}
	for _, n := range names {
		g, _ := Lookup(n)
		if _, ok := g.(SecretKeys); !ok {
			t.Errorf("%s 未声明回调验签所需的密钥", n)
		}
	}
	b, _ := json.Marshal(paytaro{}.Form())
	if !strings.HasPrefix(string(b), `{"pid":{"description":"Paytaro 应用的 App ID；","label":"App ID","type":"input"},"key":`) ||
		!strings.Contains(string(b), `"alert1":{"content":"开户`) {
		t.Errorf("表单结构或字段顺序不正确: %s", b)
	}
}

func TestEPayQrcode(t *testing.T) {
	cfg := Config{"url": "https://pay.example.com/submit.php?x=1", "pid": "1001", "key": "k3y", "type": "wxpay"}
	var form url.Values
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://pay.example.com/mapi.php" {
			t.Errorf("MAPI 地址不正确: %s", r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		return resp(200, `{"code":"1","qrcode":"","urlscheme":"weixin://wxpay/bizpayurl?pr=x"}`), nil
	})
	o := vOrder(1050)
	o.UserAgent, o.ClientIP = mobileUA, "1.2.3.4"
	res, err := ePayQrcode{}.Pay(e, o)
	if err != nil || res.Type != 1 || res.Data != "weixin://wxpay/bizpayurl?pr=x" {
		t.Fatalf("移动端应跳转: %+v %v", res, err)
	}
	m := map[string]any{}
	for k := range form {
		m[k] = form.Get(k)
	}
	if form.Get("money") != "10.50" || form.Get("device") != "mobile" || form.Get("clientip") != "1.2.3.4" ||
		form.Get("sign") != epaySign(m, "k3y") {
		t.Errorf("MAPI 下单参数不正确: %v", form)
	}
	o.UserAgent = "Mozilla/5.0 (Windows NT 10.0) MicroMessenger"
	res, _ = ePayQrcode{}.Pay(e, o)
	if res.Type != 0 || form.Get("device") != "wechat" {
		t.Errorf("桌面端应展示二维码: %+v %v", res, form)
	}

	e = newEnv(cfg, func(*http.Request) (*http.Response, error) {
		return resp(200, `{"code":-1,"msg":"商户未开通"}`), nil
	})
	if _, err := (ePayQrcode{}).Pay(e, o); gwMsg(err) != "商户未开通" {
		t.Errorf("应返回平台错误信息: %v", err)
	}
	e = newEnv(cfg, func(*http.Request) (*http.Response, error) { return resp(502, `bad gateway`), nil })
	if _, err := (ePayQrcode{}).Pay(e, o); gwMsg(err) != "支付接口网络异常或返回内容无法解析" {
		t.Errorf("非 JSON 响应: %v", err)
	}
	if _, err := (ePayQrcode{}).Pay(newEnv(Config{"url": "x", "pid": "1", "key": "k"}, nil), o); gwMsg(err) != "EPay 配置不完整：type" {
		t.Errorf("配置不完整: %v", err)
	}

	// 回调：签名、商户号、状态、订单号
	params := Params{"pid": "1001", "trade_no": "P1", "out_trade_no": vTradeNo, "money": "10.50", "trade_status": "TRADE_SUCCESS"}
	params["sign"] = strings.ToUpper(epaySign(params, "k3y"))
	ok := func(p Params) error {
		_, err := ePayQrcode{}.Notify(newEnv(cfg, nil), &Notification{Params: p})
		return err
	}
	if err := ok(params); err != nil {
		t.Errorf("大写签名也应通过: %v", err)
	}
	bad := params.without()
	bad["pid"] = "1002"
	bad["sign"] = epaySign(bad, "k3y")
	if ok(bad) == nil {
		t.Error("商户号不一致应拒绝")
	}
	bad = params.without()
	bad["trade_status"] = "WAIT_BUYER_PAY"
	bad["sign"] = epaySign(bad, "k3y")
	if ok(bad) == nil {
		t.Error("未支付状态应拒绝")
	}
	bad = params.without()
	bad["money"] = "0.01"
	if ok(bad) == nil {
		t.Error("篡改金额应验签失败")
	}
}

func TestEPaySubmitQrcode(t *testing.T) {
	cfg := Config{"url": "https://pay.example.com", "pid": "1001", "key": "k3y"}
	hops := 0
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		hops++
		switch r.URL.Path {
		case "/submit.php":
			if r.URL.Query().Get("type") != "alipay" || r.URL.Query().Get("money") != "10.50" {
				t.Errorf("submit 参数不正确: %s", r.URL.RawQuery)
			}
			return resp(302, "", "Location", "/cashier/1", "Set-Cookie", "sid=abc; Path=/"), nil
		case "/cashier/1":
			if c, _ := r.Cookie("sid"); c == nil {
				t.Error("跳转时应保持会话 Cookie")
			}
			return resp(200, `<script>window.location.href='step2?id=1';</script>`), nil
		case "/cashier/step2":
			return resp(200, `<div id="qr"></div><script>var code_url = "https://qr.alipay.com/abc123";</script>`), nil
		}
		return resp(404, ""), nil
	})
	res, err := ePaySubmitQrcode{}.Pay(e, vOrder(1050))
	if err != nil || res.Type != 0 || res.Data != "https://qr.alipay.com/abc123" || hops != 3 {
		t.Fatalf("应跟随跳转解析到二维码: %+v %v hops=%d", res, err, hops)
	}
	e = newEnv(cfg, func(*http.Request) (*http.Response, error) { return resp(200, `<html>维护中</html>`), nil })
	if _, err := (ePaySubmitQrcode{}).Pay(e, vOrder(1050)); err == nil {
		t.Error("未解析到二维码应报错")
	}

	params := Params{"pid": "1001", "trade_no": "P1", "out_trade_no": vTradeNo, "money": "10.50", "trade_status": "TRADE_SUCCESS"}
	params["sign"] = epaySign(params, "k3y")
	paid, err := ePaySubmitQrcode{}.Notify(newEnv(cfg, nil), &Notification{Params: params})
	if err != nil || paid.Amount == nil || *paid.Amount != 1050 {
		t.Errorf("回调应带上支付金额: %+v %v", paid, err)
	}
}

func TestPaytaroQR(t *testing.T) {
	cfg := Config{"pid": "app1", "key": "sec", "method_uuid": "m-1"}
	reply := `{"uuid":"inv 1","payment":{"type":"usdt","data":"TXYZ"}}`
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-App-Secret") != "sec" || r.URL.String() != paytaroAPI+"/v1/invoice/pay" {
			t.Errorf("请求头或地址不正确: %s", r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"order_amount":10.5`) {
			t.Errorf("金额不正确: %s", b)
		}
		return resp(200, reply), nil
	})
	o := vOrder(1050)
	o.NotifyURL = "https://api.example.com:8443/gvb/payment/notify/PaytaroQR/u1"
	res, err := paytaroQR{}.Pay(e, o)
	if err != nil || res.Type != 1 || res.Data != "https://api.example.com:8443/paytaro-qr/pay.php?uuid=inv%201" {
		t.Errorf("加密货币应跳转到面板域名下的支付页: %+v %v", res, err)
	}
	reply = `{"uuid":"inv1","payment":{"type":"alipay","link_type":"h5","data":"https://qr.alipay.com/x","mobile_url":"alipays://x"}}`
	if res, _ := (paytaroQR{}).Pay(e, o); res.Type != 0 || res.Data != "https://qr.alipay.com/x" {
		t.Errorf("桌面端支付宝 h5 应展示二维码: %+v", res)
	}
	o.UserAgent = mobileUA
	if res, _ := (paytaroQR{}).Pay(e, o); res.Type != 1 || res.Data != "alipays://x" {
		t.Errorf("移动端应跳转 mobile_url: %+v", res)
	}
	e = newEnv(cfg, func(*http.Request) (*http.Response, error) { return resp(400, `{"error":"余额不足"}`), nil })
	if _, err := (paytaroQR{}).Pay(e, o); gwMsg(err) != "Paytaro：余额不足" {
		t.Errorf("错误信息: %v", err)
	}

	n := notifyReq("POST", "", "application/json", `{"merchant_no":"`+vTradeNo+`","status":"PAID","transaction_no":"T9"}`, "X-App-Secret", "sec")
	if paid, err := (paytaroQR{}).Notify(newEnv(cfg, nil), n); err != nil || paid.CallbackNo != "T9" {
		t.Errorf("回调失败: %+v %v", paid, err)
	}
	n.Header.Set("X-App-Secret", "bad")
	if _, err := (paytaroQR{}).Notify(newEnv(cfg, nil), n); err == nil {
		t.Error("密钥不一致应拒绝")
	}
}

func TestUSDTGateways(t *testing.T) {
	// BEPUSDT 回调状态
	cfg := Config{"bepusdt_apitoken": "tok"}
	mk := func(status string) *Notification {
		p := Params{"trade_id": "T1", "order_id": vTradeNo, "status": json.Number(status)}
		p["signature"] = md5hex(rawQuery(p) + "tok")
		return &Notification{Params: p}
	}
	if paid, err := (bepusdt{}).Notify(newEnv(cfg, nil), mk("2")); err != nil || paid.Reply != "ok" {
		t.Errorf("BEPUSDT 成功回调: %+v %v", paid, err)
	}
	if _, err := (bepusdt{}).Notify(newEnv(cfg, nil), mk("1")); err == nil {
		t.Error("BEPUSDT 未支付状态应拒绝")
	}

	// Epusdt 指定网络时切换链路
	var paths []string
	e := newEnv(Config{"epusdt_url": "https://e.example.com", "epusdt_token": "tok", "epusdt_network": " BSC "},
		func(r *http.Request) (*http.Response, error) {
			paths = append(paths, r.URL.Path)
			if r.URL.Path == "/pay/switch-network" {
				b, _ := io.ReadAll(r.Body)
				if string(b) != `{"network":"bsc","token":"usdt","trade_id":"TR1"}` {
					t.Errorf("切换网络参数: %s", b)
				}
				return resp(200, `{"status_code":200,"data":{"payment_url":"https://e.example.com/bsc"}}`), nil
			}
			return resp(200, `{"status_code":200,"data":{"trade_id":"TR1","payment_url":"https://e.example.com/x"}}`), nil
		})
	if res, err := (epusdt{}).Pay(e, vOrder(1050)); err != nil || res.Data != "https://e.example.com/bsc" || len(paths) != 2 {
		t.Errorf("Epusdt 切换网络: %+v %v %v", res, err, paths)
	}
	p := Params{"order_id": vTradeNo, "trade_id": "T1", "amount": json.Number("10.5"), "status": json.Number("2")}
	p["signature"] = strings.ToUpper(epusdtSign(p, "tok"))
	if _, err := (epusdt{}).Notify(newEnv(Config{"epusdt_token": "tok"}, nil), &Notification{Params: p}); err != nil {
		t.Errorf("Epusdt 回调: %v", err)
	}

	// TokenPay 下单
	e = newEnv(Config{"token_pay_url": "https://t.example.com", "token_pay_apitoken": "tok", "token_pay_currency": "TRX"},
		func(r *http.Request) (*http.Response, error) {
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			if m["OrderUserKey"] != "7" || m["ActualAmount"] != 10.5 || m["Signature"] == "" {
				t.Errorf("TokenPay 下单参数: %v", m)
			}
			return resp(200, `{"success":false,"message":"币种不支持"}`), nil
		})
	if _, err := (tokenPay{}).Pay(e, vOrder(1050)); gwMsg(err) != "Failed to create order. Error: 币种不支持" {
		t.Errorf("TokenPay 错误信息: %v", err)
	}
}

func TestWebhookGateways(t *testing.T) {
	// BTCPay：验签后查询发票，只有已结算的发票才开通
	status := "New"
	btc := Config{"btcpay_url": "https://btc.example.com/", "btcpay_storeId": "S1", "btcpay_api_key": "ak", "btcpay_webhook_key": "wk"}
	e := newEnv(btc, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/stores/S1/invoices/INV1" || r.Header.Get("Authorization") != "token ak" {
			t.Errorf("发票查询请求不正确: %s", r.URL)
		}
		return resp(200, `{"status":"`+status+`","metadata":{"orderId":"`+vTradeNo+`"}}`), nil
	})
	body := `{"type":"InvoiceCreated","invoiceId":"INV1"}`
	n := notifyReq("POST", "", "application/json", body, "Btcpay-Sig", "sha256="+hmacSHA256([]byte("wk"), []byte(body)))
	if _, err := (btcPay{}).Notify(e, n); err == nil {
		t.Error("未结算的发票不应开通（原项目漏洞）")
	}
	status = "Settled"
	if paid, err := (btcPay{}).Notify(e, n); err != nil || paid.TradeNo != vTradeNo || paid.CallbackNo != "INV1" {
		t.Errorf("BTCPay 回调: %+v %v", paid, err)
	}
	n.Header.Set("Btcpay-Sig", "sha256=00")
	if _, err := (btcPay{}).Notify(e, n); err == nil {
		t.Error("BTCPay 签名错误应拒绝")
	}

	// Coinbase：只接受已确认的付款事件
	cb := Config{"coinbase_webhook_key": "wk"}
	for typ, want := range map[string]bool{"charge:created": false, "charge:pending": false, "charge:confirmed": true, "charge:resolved": true} {
		body := `{"event":{"id":"E1","type":"` + typ + `","data":{"metadata":{"outTradeNo":"` + vTradeNo + `"}}}}`
		n := notifyReq("POST", "", "application/json", body, "X-Cc-Webhook-Signature", hmacSHA256([]byte("wk"), []byte(body)))
		paid, err := coinbase{}.Notify(newEnv(cb, nil), n)
		if (err == nil) != want || (want && paid.TradeNo != vTradeNo) {
			t.Errorf("Coinbase %s: %+v %v", typ, paid, err)
		}
	}

	// CoinPayments：原始请求体 HMAC 与原项目的重新编码 HMAC 均可
	cp := Config{"coinpayments_merchant_id": "m1 ", "coinpayments_ipn_secret": "ipn"}
	raw := "merchant=m1&item_number=" + vTradeNo + "&txn_id=TX&status=100&note="
	for _, sig := range []string{
		hmacSHA512([]byte("ipn"), []byte(raw)),
		hmacSHA512([]byte("ipn"), []byte("item_number="+vTradeNo+"&merchant=m1&status=100&txn_id=TX")),
	} {
		n := notifyReq("POST", "", "application/x-www-form-urlencoded", raw, "Hmac", sig)
		if paid, err := (coinPayments{}).Notify(newEnv(cp, nil), n); err != nil || paid.Reply != "IPN OK" {
			t.Errorf("CoinPayments 回调: %+v %v", paid, err)
		}
	}
	pending := "merchant=m1&item_number=N&txn_id=TX&status=1"
	n = notifyReq("POST", "", "application/x-www-form-urlencoded", pending, "Hmac", hmacSHA512([]byte("ipn"), []byte(pending)))
	if _, err := (coinPayments{}).Notify(newEnv(cp, nil), n); err == nil {
		t.Error("CoinPayments 未完成状态应拒绝")
	}
}

func TestAlipayPay(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PrivateKey(key)
	cfg := Config{"app_id": "app1", "private_key": base64.StdEncoding.EncodeToString(der)}
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		sig, _ := base64.StdEncoding.DecodeString(q.Get("sign"))
		m := map[string]any{}
		for k := range q {
			if k != "sign" {
				m[k] = q.Get(k)
			}
		}
		d := sha256.Sum256([]byte(alipayQuery(m)))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, d[:], sig) != nil {
			t.Error("请求签名无法通过验证")
		}
		if q.Get("timestamp") != e2time() || q.Get("biz_content") != `{"out_trade_no":"`+vTradeNo+`","subject":"Gvb - 订阅","total_amount":10.5}` {
			t.Errorf("请求参数: %v", q)
		}
		return resp(200, `{"alipay_trade_precreate_response":{"code":"10000","msg":"Success","qr_code":"https://qr.alipay.com/bax1"},"sign":"x"}`), nil
	})
	if res, err := (alipayF2F{}).Pay(e, vOrder(1050)); err != nil || res.Type != 0 || res.Data != "https://qr.alipay.com/bax1" {
		t.Errorf("当面付下单: %+v %v", res, err)
	}
	e = newEnv(cfg, func(*http.Request) (*http.Response, error) {
		return resp(200, `{"alipay_trade_precreate_response":{"code":"40004","msg":"Business Failed","sub_msg":"交易已经关闭"}}`), nil
	})
	if _, err := (alipayF2F{}).Pay(e, vOrder(1050)); gwMsg(err) != "交易已经关闭" {
		t.Errorf("应返回支付宝错误信息: %v", err)
	}
	pem := "-----BEGIN PRIVATE KEY-----\n"
	p8, _ := x509.MarshalPKCS8PrivateKey(key)
	pem += base64.StdEncoding.EncodeToString(p8) + "\n-----END PRIVATE KEY-----\n"
	if _, err := parsePrivateKey(pem); err != nil {
		t.Errorf("应兼容 PKCS#8 PEM 私钥: %v", err)
	}
}

func e2time() string { return newEnv(nil, nil).Now.Format("2006-01-02 15:04:05") }

func TestWechatNative(t *testing.T) {
	cfg := Config{"app_id": "wx1", "mch_id": "m1", "api_key": "apikey"}
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		p := parseWechatXML(b)
		if p["sign"] != wechatSign(p, "apikey", "MD5") || p["total_fee"] != "1050" || p["trade_type"] != "NATIVE" {
			t.Errorf("统一下单请求不正确: %v", p)
		}
		return resp(200, `<xml><return_code><![CDATA[SUCCESS]]></return_code><result_code>SUCCESS</result_code><code_url><![CDATA[weixin://wxpay/bizpayurl?pr=abc]]></code_url></xml>`), nil
	})
	if res, err := (wechatPayNative{}).Pay(e, vOrder(1050)); err != nil || res.Data != "weixin://wxpay/bizpayurl?pr=abc" {
		t.Errorf("统一下单: %+v %v", res, err)
	}

	p := map[string]string{"appid": "wx1", "mch_id": "m1", "out_trade_no": vTradeNo, "transaction_id": "42",
		"result_code": "SUCCESS", "return_code": "SUCCESS", "total_fee": "1050", "attach": ""}
	p["sign"] = wechatSign(p, "apikey", "MD5")
	n := &Notification{Body: wechatXML(p)}
	paid, err := wechatPayNative{}.Notify(newEnv(cfg, nil), n)
	if err != nil || paid.CallbackNo != "42" || paid.Reply != wechatReplyOK {
		t.Errorf("微信回调: %+v %v", paid, err)
	}
	p["total_fee"] = "1"
	if _, err := (wechatPayNative{}).Notify(newEnv(cfg, nil), &Notification{Body: wechatXML(p)}); err == nil {
		t.Error("篡改金额应验签失败")
	}
}

func stripeSig(secret string, ts int64, body string) string {
	t := strconv.FormatInt(ts, 10)
	return "t=" + t + ",v1=" + hmacSHA256([]byte(secret), []byte(t+"."+body)) + ",v0=ignored"
}

func TestStripe(t *testing.T) {
	cfg := Config{"currency": "usd", "stripe_sk_live": "sk_x", "stripe_webhook_key": "whsec_x", "payment_method": "alipay"}
	var calls []string
	e := newEnv(cfg, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Host+r.URL.Path)
		switch {
		case r.URL.Host == "api.exchangerate-api.com":
			return resp(500, ""), nil
		case r.URL.Host == "api.frankfurter.app":
			return resp(200, `{"rates":{"USD":0.1389}}`), nil
		case r.URL.Path == "/v1/payment_methods":
			return resp(200, `{"id":"pm_1"}`), nil
		case r.URL.Path == "/v1/payment_intents":
			if r.Header.Get("Authorization") != "Bearer sk_x" {
				t.Error("缺少 Stripe 密钥")
			}
			b, _ := io.ReadAll(r.Body)
			f, _ := url.ParseQuery(string(b))
			if f.Get("amount") != "145" || f.Get("payment_method") != "pm_1" || f.Get("metadata[out_trade_no]") != vTradeNo ||
				f.Get("statement_descriptor") != "user-#7-45678901" {
				t.Errorf("PaymentIntent 参数: %v", f)
			}
			return resp(200, `{"next_action":{"alipay_handle_redirect":{"url":"https://stripe.example/alipay"}}}`), nil
		}
		return resp(404, `{"error":{"message":"no"}}`), nil
	})
	res, err := stripeAll{}.Pay(e, vOrder(1050))
	if err != nil || res.Type != 1 || res.Data != "https://stripe.example/alipay" || len(calls) != 4 {
		t.Errorf("StripeALL 支付宝（汇率接口回退）: %+v %v %v", res, err, calls)
	}
	if res, err := (stripeIntent{method: "alipay"}).Pay(e, vOrder(1050)); err != nil || res.Data != "https://stripe.example/alipay" {
		t.Errorf("StripeAlipay 改用 PaymentIntent: %+v %v", res, err)
	}
	down := newEnv(cfg, func(*http.Request) (*http.Response, error) { return nil, errors.New("down") })
	if _, err := (stripeCredit{}).Pay(down, vOrder(1050)); gwMsg(err) != msgExchangeFailed {
		t.Errorf("汇率接口全部失败: %v", err)
	}
	cny := newEnv(Config{"currency": "cny", "stripe_sk_live": "sk_x"}, func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		if f, _ := url.ParseQuery(string(b)); f.Get("amount") != "1050" || f.Get("source") != "tok_1" {
			t.Errorf("扣款参数: %v", f)
		}
		return resp(200, `{"paid":true}`), nil
	})
	o := vOrder(1050)
	o.StripeToken = "tok_1"
	if res, err := (stripeCredit{}).Pay(cny, o); err != nil || res.Type != 2 || res.Data != true {
		t.Errorf("StripeCredit 扣款: %+v %v", res, err)
	}

	now := newEnv(nil, nil).Now.Unix()
	ev := `{"type":"payment_intent.succeeded","data":{"object":{"id":"pi_1","status":"succeeded","metadata":{"out_trade_no":"` + vTradeNo + `"}}}}`
	check := func(g Gateway, body, sig string) (*Paid, error) {
		return g.Notify(newEnv(cfg, nil), notifyReq("POST", "", "application/json", body, "Stripe-Signature", sig))
	}
	if paid, err := check(stripeAll{}, ev, stripeSig("whsec_x", now-10, ev)); err != nil || paid.CallbackNo != "pi_1" {
		t.Errorf("Stripe 回调: %+v %v", paid, err)
	}
	if _, err := check(stripeAll{}, ev, stripeSig("whsec_x", now-400, ev)); err == nil {
		t.Error("超出时间容差的签名应拒绝（防重放）")
	}
	if _, err := check(stripeAll{}, ev, stripeSig("whsec_bad", now, ev)); err == nil {
		t.Error("签名错误应拒绝")
	}
	if _, err := check(stripeCredit{}, ev, stripeSig("whsec_x", now, ev)); err == nil {
		t.Error("StripeCredit 不处理 PaymentIntent 事件")
	}
	charge := `{"type":"charge.succeeded","data":{"object":{"id":"ch_1","status":"succeeded","metadata":{"out_trade_no":"` + vTradeNo + `"}}}}`
	if paid, err := check(stripeCredit{}, charge, stripeSig("whsec_x", now, charge)); err != nil || paid.CallbackNo != "ch_1" {
		t.Errorf("StripeCredit 回调: %+v %v", paid, err)
	}
	sess := `{"type":"checkout.session.completed","data":{"object":{"payment_status":"unpaid","client_reference_id":"x","payment_intent":"pi"}}}`
	if _, err := check(stripeCheckout{}, sess, stripeSig("whsec_x", now, sess)); err == nil {
		t.Error("未支付的 Checkout 会话不应开通")
	}
}
