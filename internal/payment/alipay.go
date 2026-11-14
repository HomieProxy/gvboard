package payment

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

func init() { Register("AlipayF2F", alipayF2F{}) }

const alipayGateway = "https://openapi.alipay.com/gateway.do"

type alipayF2F struct{}

func (alipayF2F) Form() Form {
	return Form{
		input("app_id", "支付宝APPID", ""),
		input("private_key", "支付宝私钥", ""),
		input("public_key", "支付宝公钥", ""),
		input("product_name", "自定义商品名称", "将会体现在支付宝账单中"),
	}
}

var rePEMArmor = regexp.MustCompile(`-----[A-Z ]+-----|\s+`)

// keyBytes 去掉 PEM 头尾与空白后按 Base64 解码，单行密钥与 PEM 格式均可。
func keyBytes(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(rePEMArmor.ReplaceAllString(s, ""))
}

func parsePrivateKey(s string) (*rsa.PrivateKey, error) {
	der, err := keyBytes(s)
	if err != nil {
		return nil, err
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("不是 RSA 私钥")
	}
	return rk, nil
}

func parsePublicKey(s string) (*rsa.PublicKey, error) {
	der, err := keyBytes(s)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("不是 RSA 公钥")
	}
	return rk, nil
}

// alipayQuery 键排序后按 k=v 原样拼接（空值也参与，与原项目一致）。
func alipayQuery(p map[string]any) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + phpStr(p[k])
	}
	return strings.Join(parts, "&")
}

func (alipayF2F) Pay(e *Env, o *Order) (*Result, error) {
	subject := e.Config.Str("product_name")
	if !e.Config.Has("product_name") {
		subject = e.AppName + " - 订阅"
	}
	biz := marshalJSON(map[string]any{
		"subject":      subject,
		"out_trade_no": o.TradeNo,
		"total_amount": jsonAmount(phpDiv100(o.TotalAmount)),
	})
	const method = "alipay.trade.precreate"
	p := map[string]any{
		"app_id":         e.Config.Str("app_id"),
		"method":         method,
		"charset":        "UTF-8",
		"sign_type":      "RSA2",
		"timestamp":      e.Now.Format("2006-01-02 15:04:05"),
		"biz_content":    string(biz),
		"version":        "1.0",
		"_input_charset": "UTF-8",
	}
	if o.NotifyURL != "" {
		p["notify_url"] = o.NotifyURL
	}
	key, err := parsePrivateKey(e.Config.Str("private_key"))
	if err != nil {
		return nil, gatewayError("支付宝私钥格式错误")
	}
	digest := sha256.Sum256([]byte(alipayQuery(p)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, gatewayError("支付宝私钥格式错误")
	}
	q := url.Values{}
	for k, v := range p {
		q.Set(k, phpStr(v))
	}
	q.Set("sign", base64.StdEncoding.EncodeToString(sig))

	_, b, err := call(e, http.MethodGet, alipayGateway+"?"+q.Encode(), nil, nil)
	res, _ := decodeObject(b)[strings.ReplaceAll(method, ".", "_")+"_response"].(map[string]any)
	if err != nil || res == nil {
		return nil, gatewayError("从支付宝请求失败")
	}
	if res["msg"] != "Success" {
		return nil, gatewayError(phpStr(res["sub_msg"]))
	}
	if res["qr_code"] == nil {
		return nil, gatewayError("获取付款二维码失败")
	}
	return &Result{Type: 0, Data: res["qr_code"]}, nil
}

func (alipayF2F) Notify(e *Env, n *Notification) (*Paid, error) {
	p := n.Params
	if p["trade_status"] != "TRADE_SUCCESS" {
		return nil, errVerify
	}
	sig, err := base64.StdEncoding.DecodeString(p.Str("sign"))
	if err != nil {
		return nil, errVerify
	}
	pub, err := parsePublicKey(e.Config.Str("public_key"))
	if err != nil {
		return nil, errVerify
	}
	digest := sha256.Sum256([]byte(alipayQuery(p.without("sign", "sign_type"))))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) != nil {
		return nil, errVerify
	}
	return &Paid{TradeNo: p.Str("out_trade_no"), CallbackNo: p.Str("trade_no")}, nil
}
