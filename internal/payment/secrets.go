package payment

func (ePay) SecretKeys() []string             { return []string{"key"} }
func (ePay2) SecretKeys() []string            { return []string{"key", "pid"} }
func (ePayQrcode) SecretKeys() []string       { return []string{"key", "pid"} }
func (ePaySubmitQrcode) SecretKeys() []string { return []string{"key", "pid"} }
func (paytaro) SecretKeys() []string          { return []string{"key"} }
func (paytaroQR) SecretKeys() []string        { return []string{"key"} }
func (bepusdt) SecretKeys() []string          { return []string{"bepusdt_apitoken"} }
func (epusdt) SecretKeys() []string           { return []string{"epusdt_token"} }
func (tokenPay) SecretKeys() []string         { return []string{"token_pay_apitoken"} }
func (btcPay) SecretKeys() []string           { return []string{"btcpay_webhook_key"} }
func (coinbase) SecretKeys() []string         { return []string{"coinbase_webhook_key"} }
func (coinPayments) SecretKeys() []string     { return []string{"coinpayments_ipn_secret"} }
func (alipayF2F) SecretKeys() []string        { return []string{"public_key"} }
func (wechatPayNative) SecretKeys() []string  { return []string{"api_key"} }
func (stripeAll) SecretKeys() []string        { return []string{"stripe_webhook_key"} }
func (stripeIntent) SecretKeys() []string     { return []string{"stripe_webhook_key"} }
func (stripeCheckout) SecretKeys() []string   { return []string{"stripe_webhook_key"} }
func (stripeCredit) SecretKeys() []string     { return []string{"stripe_webhook_key"} }

// missingSecret 网关声明的密钥配置是否有为空的。
func missingSecret(g Gateway, c Config) bool {
	sk, ok := g.(SecretKeys)
	if !ok {
		return false
	}
	for _, k := range sk.SecretKeys() {
		if phpTrim(c.Str(k)) == "" {
			return true
		}
	}
	return false
}
