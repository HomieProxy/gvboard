package payment

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gvboard/internal/util"
)

func init() { Register("WechatPayNative", wechatPayNative{}) }

const (
	wechatUnifiedOrder = "https://api.mch.weixin.qq.com/pay/unifiedorder"
	wechatReplyOK      = "<xml><return_code><![CDATA[SUCCESS]]></return_code><return_msg><![CDATA[OK]]></return_msg></xml>"
)

type wechatPayNative struct{}

func (wechatPayNative) Form() Form {
	return Form{
		input("app_id", "APPID", "绑定微信支付商户的APPID"),
		input("mch_id", "商户号", "微信支付商户号"),
		input("api_key", "APIKEY(v1)", ""),
	}
}

// wechatSign 微信支付 v2 签名：去掉 sign 与空值，键排序后拼接 &key=，MD5 或 HMAC-SHA256，大写。
func wechatSign(p map[string]string, key, signType string) string {
	keys := make([]string, 0, len(p))
	for k, v := range p {
		if k != "sign" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + p[k]
	}
	s := strings.Join(parts, "&") + "&key=" + key
	if signType == "HMAC-SHA256" {
		m := hmac.New(sha256.New, []byte(key))
		m.Write([]byte(s))
		return strings.ToUpper(hex.EncodeToString(m.Sum(nil)))
	}
	return strings.ToUpper(md5hex(s))
}

func wechatXML(p map[string]string) []byte {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("<xml>")
	for _, k := range keys {
		v := p[k]
		if _, err := strconv.ParseInt(v, 10, 64); err == nil {
			b.WriteString("<" + k + ">" + v + "</" + k + ">")
		} else {
			b.WriteString("<" + k + "><![CDATA[" + strings.ReplaceAll(v, "]]>", "]]]]><![CDATA[>") + "]]></" + k + ">")
		}
	}
	b.WriteString("</xml>")
	return b.Bytes()
}

// parseWechatXML 解析一级 <xml> 下的子元素文本，非法时返回 nil。
func parseWechatXML(b []byte) map[string]string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	out := map[string]string{}
	depth := 0
	var key string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				key = t.Name.Local
				text.Reset()
			}
		case xml.CharData:
			if depth == 2 {
				text.Write(t)
			}
		case xml.EndElement:
			if depth == 2 {
				out[key] = text.String()
			}
			depth--
		}
	}
	return out
}

func (wechatPayNative) Pay(e *Env, o *Order) (*Result, error) {
	key := e.Config.Str("api_key")
	p := map[string]string{
		"appid":            e.Config.Str("app_id"),
		"mch_id":           e.Config.Str("mch_id"),
		"nonce_str":        util.RandomChar(32, false),
		"body":             o.TradeNo,
		"out_trade_no":     o.TradeNo,
		"total_fee":        strconv.FormatInt(o.TotalAmount, 10),
		"spbill_create_ip": "0.0.0.0",
		"fee_type":         "CNY",
		"notify_url":       o.NotifyURL,
		"trade_type":       "NATIVE",
	}
	p["sign"] = wechatSign(p, key, "MD5")
	_, b, err := call(e, http.MethodPost, wechatUnifiedOrder, wechatXML(p), map[string]string{"Content-Type": "text/xml"})
	res := parseWechatXML(b)
	if err != nil || res == nil {
		return nil, gatewayError("Payment gateway request failed")
	}
	if res["return_code"] != "SUCCESS" {
		return nil, gatewayError(res["return_msg"])
	}
	if res["code_url"] == "" {
		msg := res["err_code_des"]
		if msg == "" {
			msg = "Payment gateway request failed"
		}
		return nil, gatewayError(msg)
	}
	return &Result{Type: 0, Data: res["code_url"]}, nil
}

// Notify 校验签名且 result_code 为 SUCCESS 时视为已支付（与 Omnipay isPaid 一致）。
// 原项目把应答 XML 错放在 pay() 的返回值里，回调实际只返回 success；这里按微信要求返回 XML。
func (wechatPayNative) Notify(e *Env, n *Notification) (*Paid, error) {
	p := parseWechatXML(n.Body)
	if p == nil || p["sign"] == "" {
		return nil, errVerify
	}
	signType := p["sign_type"]
	if signType == "" {
		signType = "MD5"
	}
	if !hashEquals(wechatSign(p, e.Config.Str("api_key"), signType), p["sign"]) || p["result_code"] != "SUCCESS" {
		return nil, errVerify
	}
	return &Paid{TradeNo: p["out_trade_no"], CallbackNo: p["transaction_id"], Reply: wechatReplyOK}, nil
}
