package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// 以下期望值由原项目的 PHP 支付类（v2board/app/Payments 与生产站点自定义支付类）在 PHP 8.2 中生成。

const (
	vTradeNo   = "2026111312345678901"
	vNotifyURL = "https://api.example.com/gvb/payment/notify/EPay/abcd1234"
	vReturnURL = "https://web.example.com/#/order/2026111312345678901"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string, header ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func newEnv(cfg Config, f roundTrip) *Env {
	if f == nil {
		f = func(r *http.Request) (*http.Response, error) {
			panic("意外的外部请求: " + r.URL.String())
		}
	}
	return &Env{
		Ctx: context.Background(), Config: cfg, HTTP: &http.Client{Transport: f},
		Now: time.Unix(1794000000, 0), AppName: "Gvb",
	}
}

func vOrder(amount int64) *Order {
	return &Order{TradeNo: vTradeNo, TotalAmount: amount, UserID: 7, NotifyURL: vNotifyURL, ReturnURL: vReturnURL}
}

func TestPayURLVectors(t *testing.T) {
	const q = "&name=2026111312345678901&notify_url=https%3A%2F%2Fapi.example.com%2Fgvb%2Fpayment%2Fnotify%2FEPay%2Fabcd1234" +
		"&out_trade_no=2026111312345678901&pid=1001&return_url=https%3A%2F%2Fweb.example.com%2F%23%2Forder%2F2026111312345678901"
	const pq = "https://v3.paytaro.com/submit.php?pid=app1&type=alipay&out_trade_no=2026111312345678901" +
		"&notify_url=https%3A%2F%2Fapi.example.com%2Fgvb%2Fpayment%2Fnotify%2FEPay%2Fabcd1234" +
		"&return_url=https%3A%2F%2Fweb.example.com%2F%23%2Forder%2F2026111312345678901&name=2026111312345678901"
	epay := Config{"url": "https://pay.example.com", "pid": "1001", "key": "k3y", "type": "alipay"}
	epay2 := Config{"url": "https://pay.example.com", "pid": "1001", "key": "k3y", "payment_method": "wxpay"}
	pay := Config{"pid": "app1", "key": "sec"}
	cases := []struct {
		g    Gateway
		cfg  Config
		amt  int64
		want string
	}{
		{ePay{}, epay, 1050, "https://pay.example.com/submit.php?money=10.5" + q + "&type=alipay&sign=306f65e8968dc77246e51fbbbcf4bc38&sign_type=MD5"},
		{ePay{}, epay, 1000, "https://pay.example.com/submit.php?money=10" + q + "&type=alipay&sign=0e7222ed26459a625f1c319996b39eea&sign_type=MD5"},
		{ePay{}, epay, 1, "https://pay.example.com/submit.php?money=0.01" + q + "&type=alipay&sign=734858c4ce12f501f01d47f7823a4b78&sign_type=MD5"},
		{ePay{}, epay, 123456, "https://pay.example.com/submit.php?money=1234.56" + q + "&type=alipay&sign=2c7be4dabdb35c6ed8aacd6b3c7015e1&sign_type=MD5"},
		{ePay{}, Config{"url": "https://pay.example.com", "pid": "1001", "key": "k3y", "type": nil}, 1050,
			"https://pay.example.com/submit.php?money=10.5" + q + "&sign=71a5cc3f1026bf85633a0d1fdd0c7f81&sign_type=MD5"},
		{ePay2{}, epay2, 1050, "https://pay.example.com/submit.php?money=10.5" + q + "&type=wxpay&sign=57af228c60bda9894e9b47f7649cf822&sign_type=MD5"},
		{ePay2{}, epay2, 1000, "https://pay.example.com/submit.php?money=10" + q + "&type=wxpay&sign=134a7124debbd956d011dcb37f9e9c9b&sign_type=MD5"},
		{ePay2{}, epay2, 1, "https://pay.example.com/submit.php?money=0.01" + q + "&type=wxpay&sign=e7432d60e542c25e88a38b648603900f&sign_type=MD5"},
		{ePay2{}, epay2, 123456, "https://pay.example.com/submit.php?money=1234.56" + q + "&type=wxpay&sign=d1e6b6e90a9e7a3770ccdfd816033696&sign_type=MD5"},
		{paytaro{}, pay, 1050, pq + "&money=10.50&sign=9f700a4123446c7f1b55bfd32ce5852b&sign_type=MD5"},
		{paytaro{}, pay, 1000, pq + "&money=10.00&sign=2425813be9c10acc931561ff9bd9f994&sign_type=MD5"},
		{paytaro{}, pay, 1, pq + "&money=0.01&sign=0554891e278c47b9366ec6286e8d74a7&sign_type=MD5"},
		{paytaro{}, pay, 123456, pq + "&money=1234.56&sign=0fa2597348b8b7cd2586babab402c30c&sign_type=MD5"},
	}
	for i, c := range cases {
		res, err := c.g.Pay(newEnv(c.cfg, nil), vOrder(c.amt))
		if err != nil || res.Type != 1 || res.Data != c.want {
			t.Errorf("用例 %d: 支付链接与原项目不一致\n得到 %v %v\n期望 %s", i, res, err, c.want)
		}
	}
}

// notifyReq 构造回调请求并按 Laravel 规则解析参数。
func notifyReq(method, query, contentType, body string, header ...string) *Notification {
	r := httptest.NewRequest(method, "/n?"+query, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	return &Notification{Params: parseParams(r, []byte(body)), Body: []byte(body), Header: r.Header}
}

func TestSignVectors(t *testing.T) {
	// EPay 回调：参数含空格、加号、反斜杠、中文与空值；空值经 Laravel 转为 null 后不参与签名
	form := "pid=1001&trade_no=P20261113&out_trade_no=2026111312345678901&type=alipay&name=" +
		"VIP+%E5%A5%97%E9%A4%90%2B1+a%5Cb&money=10.50&trade_status=TRADE_SUCCESS&param=&sign_type=MD5&sign=da46ecf3bfec67cee832616897d33def"
	n := notifyReq("GET", form, "", "")
	if n.Params["param"] != nil || n.Params.Str("name") != `VIP 套餐+1 a\b` {
		t.Fatalf("回调参数预处理与 Laravel 不一致: %#v", n.Params)
	}
	paid, err := ePay{}.Notify(newEnv(Config{"key": "k3y"}, nil), n)
	if err != nil || paid.TradeNo != vTradeNo || paid.CallbackNo != "P20261113" {
		t.Errorf("EPay 回调验签失败: %+v %v", paid, err)
	}
	if got := epaySign(n.Params, "k3y"); got != "a7a072ba8a65735addbb17820f1bb27c" {
		t.Errorf("EPayQrcode 签名 = %s", got)
	}

	// JSON 回调：数字按 PHP 类型参与签名
	body := `{"trade_id":"T1","order_id":"2026111312345678901","amount":10.5,"actual_amount":1.4285,"token":"TXYZ",` +
		`"block_transaction_id":"abc","status":2,"whole":10.0,"flag":true,"off":false}`
	jn := notifyReq("POST", "", "application/json", body)
	if got := md5hex(rawQuery(jn.Params) + "tok"); got != "101d3f7da7bdf3fff1d981faab500f57" {
		t.Errorf("BEPUSDT 回调签名 = %s", got)
	}
	if got := epusdtSign(jn.Params, "tok"); got != "a283d14c98861043f8431cbc26e1200c" {
		t.Errorf("Epusdt 回调签名 = %s", got)
	}
	tp := notifyReq("POST", "", "application/json",
		`{"Id":"X1","OutOrderId":"2026111312345678901","ActualAmount":10.5,"Amount":1.43,"Status":1,"PassThroughInfo":null,"Currency":"USDT_TRC20","Signature":"848a56331ae786a382cb0d2fe45b8d1d"}`)
	if paid, err := (tokenPay{}).Notify(newEnv(Config{"token_pay_apitoken": "tok"}, nil), tp); err != nil || paid.CallbackNo != "X1" || paid.Reply != "ok" {
		t.Errorf("TokenPay 回调验签失败: %+v %v", paid, err)
	}

	// CoinPayments：参数排序后按 http_build_query 重新编码
	cp := Params{"merchant": "m1", "item_number": "N1", "email": "a+b@x.com", "first_name": "John Doe", "note": "x~y*z/é", "status": "100", "txn_id": "TX"}
	wantQ := "email=a%2Bb%40x.com&first_name=John+Doe&item_number=N1&merchant=m1&note=x%7Ey%2Az%2F%C3%A9&status=100&txn_id=TX"
	if got := encodeQuery(flatten(cp, "")); got != wantQ {
		t.Errorf("http_build_query = %s", got)
	}
	m := hmac.New(sha512.New, []byte("ipn"))
	m.Write([]byte(wantQ))
	if hex.EncodeToString(m.Sum(nil)) != "66e317a53a45178208a0c0259413b8c44b1f847757aea7e78a4b31f8a3a1c39afe4f3c313ea6aced6aafb6063e117f8e7eaff597af058b40a0b1bcb5970a19df" {
		t.Error("CoinPayments HMAC 不一致")
	}

	// 数值转字符串
	if phpStr(phpDiv100(1050)) != "10.5" || phpFloat(0.1+0.2) != "0.3" {
		t.Error("PHP 浮点转字符串不一致")
	}
	if got := encodeQuery(flatten(map[string]any{"a": 0.1 + 0.2, "b": 1e20, "c": true, "d": false, "e": nil}, "")); got != "a=0.3&b=1.0E%2B20&c=1&d=0" {
		t.Errorf("http_build_query 标量转换 = %s", got)
	}
}

func TestPaySignVectors(t *testing.T) {
	var got map[string]any
	capture := func(reply string) roundTrip {
		return func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			got = nil
			_ = json.Unmarshal(b, &got)
			return resp(200, reply), nil
		}
	}
	e := newEnv(Config{"bepusdt_url": "https://b.example.com", "bepusdt_apitoken": "tok", "bepusdt_trade_type": "usdt.trc20"},
		capture(`{"status_code":200,"data":{"payment_url":"https://b.example.com/pay/1"}}`))
	if res, err := (bepusdt{}).Pay(e, vOrder(1050)); err != nil || res.Data != "https://b.example.com/pay/1" ||
		got["signature"] != "3f25e833779276dd4139d0f184d66053" || got["amount"] != 10.5 {
		t.Errorf("BEPUSDT 下单签名不一致: %v %v %v", res, err, got)
	}

	var raw string
	e = newEnv(Config{"epusdt_url": "https://e.example.com/", "epusdt_pid": "p1", "epusdt_token": "tok"},
		func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			raw = string(b)
			return resp(200, `{"status_code":200,"data":{"payment_url":"https://e.example.com/pay/1"}}`), nil
		})
	if _, err := (epusdt{}).Pay(e, vOrder(1000)); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
	if body["signature"] != "83a241fd805416424a845e3da311785d" || !strings.Contains(raw, `"amount":10,`) ||
		!strings.Contains(raw, `"notify_url":"https://api.example.com/gvb/payment/notify/EPay/abcd1234"`) {
		t.Errorf("Epusdt 下单请求与原项目不一致: %s", raw)
	}
}

// TestAlipayVector 原项目 library/AlipayF2F.php 生成的 RSA2 签名（测试专用临时密钥）。
func TestAlipayVector(t *testing.T) {
	b, err := os.ReadFile("testdata/alipay_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		PublicKey string            `json:"public_key"`
		Params    map[string]string `json:"params"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	q := make([]string, 0, len(v.Params))
	for k, val := range v.Params {
		q = append(q, phpURLEncode(k)+"="+phpURLEncode(val))
	}
	cfg := Config{"public_key": v.PublicKey}
	paid, err := alipayF2F{}.Notify(newEnv(cfg, nil), notifyReq("POST", "", "application/x-www-form-urlencoded", strings.Join(q, "&")))
	if err != nil || paid.TradeNo != vTradeNo || paid.CallbackNo != "2026111322001" {
		t.Fatalf("支付宝回调验签失败: %+v %v", paid, err)
	}
	// 篡改金额后验签失败
	v.Params["total_amount"] = "0.01"
	q = q[:0]
	for k, val := range v.Params {
		q = append(q, phpURLEncode(k)+"="+phpURLEncode(val))
	}
	if _, err := (alipayF2F{}).Notify(newEnv(cfg, nil), notifyReq("POST", "", "application/x-www-form-urlencoded", strings.Join(q, "&"))); err == nil {
		t.Error("篡改参数后应验签失败")
	}
}
