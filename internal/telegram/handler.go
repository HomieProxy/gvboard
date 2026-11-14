package telegram

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

const (
	webhookPath     = "/telegram/webhook/"
	maxUpdateBody   = 1 << 20
	secretHeader    = "X-Telegram-Bot-Api-Secret-Token"
	legacyTokenName = "access_token"
)

// Module Telegram 路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载路由。
//
// Webhook（Telegram 调用）：
//
//	POST {api}/telegram/webhook/:secret      路径与 X-Telegram-Bot-Api-Secret-Token 均需与密钥一致，否则 404
//	POST /api/v1/guest/telegram/webhook      原路径（compat.legacy_callback），access_token=md5(机器人 token)
//
// 用户端（需登录）：
//
//	GET  {api}/user/telegram                 机器人信息、绑定状态、通知偏好
//	POST {api}/user/telegram/bind            生成一键绑定链接
//	PUT  {api}/user/telegram/prefs           修改通知偏好，{"notify_order","notify_ticket","notify_remind"}
//
// 管理端：
//
//	POST telegram/apply                      按当前设置初始化机器人（命令菜单、Webhook 或长轮询）
//	GET  telegram/info                       Webhook 状态
func (m *Module) Register(r *server.Router) {
	r.API.POST(webhookPath+":secret", m.webhook)
	if r.Config.Compat.LegacyCallback {
		r.LegacyRoot.POST("/guest/telegram/webhook", m.legacyWebhook)
	}

	u := r.API.Group("/user", m.Auth.RequireUser())
	u.GET("/telegram", m.status)
	u.POST("/telegram/bind", m.bindLink)
	u.PUT("/telegram/prefs", m.updatePrefs)

	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.POST("/telegram/apply", m.apply)
	a.GET("/telegram/info", m.info)
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (m *Module) webhook(c *gin.Context) {
	secret := m.Svc.Settings.String("telegram_webhook_secret")
	if secret == "" || !m.Svc.Enabled() || !equal(c.Param("secret"), secret) || !equal(c.GetHeader(secretHeader), secret) {
		httpx.NotFound(c)
		return
	}
	m.receive(c)
}

func (m *Module) legacyWebhook(c *gin.Context) {
	token := m.Svc.Settings.String("telegram_bot_token")
	sum := md5.Sum([]byte(token))
	if token == "" || !m.Svc.Enabled() || !equal(c.Query(legacyTokenName), hex.EncodeToString(sum[:])) {
		httpx.NotFound(c)
		return
	}
	m.receive(c)
}

// receive 处理更新后总是返回 200，避免 Telegram 反复重发。
func (m *Module) receive(c *gin.Context) {
	var u Update
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxUpdateBody)).Decode(&u); err == nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), updateTimeout)
		m.Svc.HandleUpdate(ctx, u)
		cancel()
	}
	c.Status(http.StatusOK)
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func uid(c *gin.Context) int64 { return auth.Current(c).ID }

func (m *Module) status(c *gin.Context) {
	st, err := m.Svc.Status(c.Request.Context(), uid(c))
	respond(c, st, err)
}

func (m *Module) bindLink(c *gin.Context) {
	l, err := m.Svc.NewBindLink(c.Request.Context(), uid(c))
	respond(c, l, err)
}

func (m *Module) updatePrefs(c *gin.Context) {
	var in PrefsInput
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalid)
		return
	}
	respond(c, true, m.Svc.UpdatePrefs(c.Request.Context(), uid(c), in))
}

func (m *Module) apply(c *gin.Context) {
	res, err := m.Svc.Apply(c.Request.Context(), httpx.BaseURL(c))
	respond(c, res, err)
}

func (m *Module) info(c *gin.Context) {
	w, err := m.Svc.Info(c.Request.Context())
	respond(c, w, err)
}
