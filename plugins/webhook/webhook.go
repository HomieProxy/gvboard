// Package webhook 示例插件：把面板事件以 JSON POST 推送到指定地址，
// 请求头 X-Signature 为 hex(HMAC-SHA256(密钥, 请求体))。推送失败时由队列重试。
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	sdk "gvboard/pkg/plugin"
)

func init() { sdk.Register(&Plugin{}) }

// Plugin Webhook 插件。
type Plugin struct {
	client *http.Client
}

func (p *Plugin) Info() sdk.Info {
	return sdk.Info{
		Code: "webhook", Name: "Webhook 推送", Version: "1.0.0", Author: "Gvboard",
		Description: "把注册、订单、工单等事件推送到指定地址",
	}
}

func (p *Plugin) Fields() []sdk.Field {
	return []sdk.Field{
		{Key: "url", Label: "推送地址", Type: sdk.FieldString, Required: true, Help: "http:// 或 https:// 地址"},
		{Key: "secret", Label: "签名密钥", Type: sdk.FieldPassword, Help: "用于计算 X-Signature，为空时不签名"},
		{Key: "events", Label: "推送事件", Type: sdk.FieldText, Help: "每行一个事件名，为空时推送全部事件（不支持 traffic.reported）"},
	}
}

func (p *Plugin) Setup(h sdk.Host) error {
	p.client = &http.Client{Timeout: 10 * time.Second}
	for _, name := range sdk.Events() {
		if name != sdk.EventTrafficReported {
			h.On(name, func(ctx context.Context, e sdk.Event) error { return p.push(ctx, h, e, false) })
		}
	}
	h.AdminRoutes().POST("/test", func(c *gin.Context) {
		e := sdk.Event{Name: "webhook.test", Data: json.RawMessage(`{}`), Time: time.Now().Unix()}
		if err := p.push(c.Request.Context(), h, e, true); err != nil {
			sdk.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		sdk.OK(c, true)
	})
	return nil
}

// Enable 启用前检查推送地址。
func (p *Plugin) Enable(_ context.Context, h sdk.Host) error {
	return checkURL(h.Config().String("url"))
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("推送地址无效")
	}
	return nil
}

func wanted(cfg sdk.Config, event string) bool {
	list := strings.Fields(cfg.String("events"))
	return len(list) == 0 || slices.Contains(list, event)
}

func (p *Plugin) push(ctx context.Context, h sdk.Host, e sdk.Event, force bool) error {
	cfg := h.Config()
	if !force && !wanted(cfg, e.Name) {
		return nil
	}
	target := cfg.String("url")
	if err := checkURL(target); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"event": e.Name, "time": e.Time, "data": e.Data})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := cfg.String("secret"); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("推送失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("推送失败: HTTP %d", resp.StatusCode)
	}
	return nil
}
