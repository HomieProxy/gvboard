// Package captcha 人机验证，对应原项目使用的 google/recaptcha。
package captcha

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Verifier 校验前端提交的验证结果。
type Verifier interface {
	Verify(ctx context.Context, secret, response string) bool
}

// DefaultEndpoint 与原项目所用 google/recaptcha 库的默认地址一致。
const DefaultEndpoint = "https://www.google.com/recaptcha/api/siteverify"

// ReCaptcha 调用 Google reCAPTCHA 校验接口。
type ReCaptcha struct {
	Endpoint string
	Client   *http.Client
}

// NewReCaptcha 创建校验器，请求超时 10 秒。
func NewReCaptcha() *ReCaptcha {
	return &ReCaptcha{Endpoint: DefaultEndpoint, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Verify 网络错误、响应异常一律视为校验失败（与原项目一致）。
func (r *ReCaptcha) Verify(ctx context.Context, secret, response string) bool {
	if secret == "" || response == "" {
		return false
	}
	form := url.Values{"secret": {secret}, "response": {response}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return false
	}
	return out.Success
}
