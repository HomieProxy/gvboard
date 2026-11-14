// Package telegram Telegram 机器人：Bot API 客户端、Webhook 与长轮询、命令、通知（原项目
// Plugins/Telegram 与 TelegramService，增强见决策 D51）。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Client Bot API 客户端。
type Client struct {
	Base  string // 例如 https://api.telegram.org，不含末尾 /
	Token string
	HTTP  *http.Client
}

// APIError Bot API 返回的错误。
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram %s 返回错误 %d: %s", e.Method, e.Code, e.Description)
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Call 以 JSON POST 调用 method，结果解析到 out（可为 nil）。错误信息中的 token 会被隐藏。
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.do(ctx, method, "application/json", body, out)
}

// upload 以 multipart 上传文件（sendPhoto 等），fields 中的非字符串值按 JSON 编码。
func (c *Client) upload(ctx context.Context, method string, fields map[string]any, file, filename string, data []byte, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		s, ok := v.(string)
		if !ok {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			s = string(b)
		}
		if err := w.WriteField(k, s); err != nil {
			return err
		}
	}
	fw, err := w.CreateFormFile(file, filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.do(ctx, method, w.FormDataContentType(), buf.Bytes(), out)
}

func (c *Client) do(ctx context.Context, method, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/bot"+c.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.scrub(method, err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return c.scrub(method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return c.scrub(method, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("Telegram %s 响应无法解析（HTTP %d）", method, resp.StatusCode)
	}
	if !env.OK {
		e := &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
		if env.Parameters != nil {
			e.RetryAfter = env.Parameters.RetryAfter
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// scrub 网络错误中带有完整 URL，去掉其中的 token。
func (c *Client) scrub(method string, err error) error {
	msg := err.Error()
	if c.Token != "" {
		msg = strings.ReplaceAll(msg, c.Token, "***")
	}
	return fmt.Errorf("请求 Telegram %s 失败: %s", method, msg)
}

// IsNotModified 编辑消息时内容与原消息相同。
func IsNotModified(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == 400 && strings.Contains(e.Description, "message is not modified")
}

// IsAPIError 判断错误码。
func IsAPIError(err error, codes ...int) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range codes {
		if e.Code == c {
			return true
		}
	}
	return false
}

// ---------- 数据结构（只包含用到的字段） ----------

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID      int64    `json:"message_id"`
	From           *User    `json:"from"`
	Chat           Chat     `json:"chat"`
	Text           string   `json:"text"`
	ReplyToMessage *Message `json:"reply_to_message"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type ChatJoinRequest struct {
	Chat Chat `json:"chat"`
	From User `json:"from"`
}

type Update struct {
	UpdateID        int64            `json:"update_id"`
	Message         *Message         `json:"message"`
	CallbackQuery   *CallbackQuery   `json:"callback_query"`
	ChatJoinRequest *ChatJoinRequest `json:"chat_join_request"`
}

// InlineButton 内联按钮。
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboard 内联键盘。
type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

// OutMessage 发送的消息。
type OutMessage struct {
	ChatID      int64           `json:"chat_id"`
	Text        string          `json:"text"`
	ParseMode   string          `json:"parse_mode,omitempty"`
	Preview     *PreviewOptions `json:"link_preview_options,omitempty"`
	ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
}

type PreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type WebhookInfo struct {
	URL                  string `json:"url"`
	PendingUpdateCount   int    `json:"pending_update_count"`
	LastErrorDate        int64  `json:"last_error_date,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	MaxConnections       int    `json:"max_connections,omitempty"`
	HasCustomCertificate bool   `json:"has_custom_certificate"`
}

// AllowedUpdates 机器人接收的更新类型。
var AllowedUpdates = []string{"message", "callback_query", "chat_join_request"}

// ---------- 方法 ----------

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.Call(ctx, "getMe", struct{}{}, &u)
}

func (c *Client) SendMessage(ctx context.Context, m OutMessage) error {
	return c.Call(ctx, "sendMessage", m, nil)
}

// EditMessageText 原地修改消息文本与按钮。
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, m OutMessage) error {
	return c.Call(ctx, "editMessageText", map[string]any{
		"chat_id": chatID, "message_id": messageID, "text": m.Text, "parse_mode": m.ParseMode,
		"link_preview_options": m.Preview, "reply_markup": m.ReplyMarkup,
	}, nil)
}

// SendPhoto 上传 PNG 图片。
func (c *Client) SendPhoto(ctx context.Context, chatID int64, png []byte, caption string, kb *InlineKeyboard) error {
	fields := map[string]any{"chat_id": chatID, "caption": caption, "parse_mode": "HTML"}
	if kb != nil {
		fields["reply_markup"] = kb
	}
	return c.upload(ctx, "sendPhoto", fields, "photo", "qrcode.png", png, nil)
}

// AnswerCallbackQuery 应答按钮回调，text 不为空时弹出提示。
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string, alert bool) error {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
		p["show_alert"] = alert
	}
	return c.Call(ctx, "answerCallbackQuery", p, nil)
}

func (c *Client) SetWebhook(ctx context.Context, url, secret string) error {
	return c.Call(ctx, "setWebhook", map[string]any{
		"url": url, "secret_token": secret, "allowed_updates": AllowedUpdates,
	}, nil)
}

func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.Call(ctx, "deleteWebhook", struct{}{}, nil)
}

func (c *Client) GetWebhookInfo(ctx context.Context) (*WebhookInfo, error) {
	var w WebhookInfo
	return &w, c.Call(ctx, "getWebhookInfo", struct{}{}, &w)
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var out []Update
	err := c.Call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeout, "allowed_updates": AllowedUpdates,
	}, &out)
	return out, err
}

// SetMyCommands languageCode 为空时设置默认命令列表。
func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand, languageCode string) error {
	p := map[string]any{"commands": cmds}
	if languageCode != "" {
		p["language_code"] = languageCode
	}
	return c.Call(ctx, "setMyCommands", p, nil)
}

func (c *Client) ApproveChatJoinRequest(ctx context.Context, chatID, userID int64) error {
	return c.Call(ctx, "approveChatJoinRequest", map[string]any{"chat_id": chatID, "user_id": userID}, nil)
}

func (c *Client) DeclineChatJoinRequest(ctx context.Context, chatID, userID int64) error {
	return c.Call(ctx, "declineChatJoinRequest", map[string]any{"chat_id": chatID, "user_id": userID}, nil)
}
