// Package httpx 定义 Gvboard 原生 API 的统一响应格式与错误类型。
package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Body 原生 API 统一响应体。code 为 0 表示成功。
type Body struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

// Page 分页数据。
type Page struct {
	Total int64 `json:"total"`
	List  any   `json:"list"`
}

// Error 业务错误，携带 HTTP 状态码与业务码。
// Msg 同时作为翻译键，返回前按请求语言翻译，Params 为占位符参数。
type Error struct {
	Status int
	Code   int
	Msg    string
	Params map[string]any
	Err    error // 内部原因，仅写日志，不返回给客户端
}

// With 返回带占位符参数的副本。
func (e *Error) With(params map[string]any) *Error {
	cp := *e
	cp.Params = params
	return &cp
}

// Translator 错误信息翻译函数。
type Translator func(c *gin.Context, key string, params map[string]any) string

var translator Translator

// SetTranslator 注册错误信息翻译函数，必须在开始处理请求前调用。
func SetTranslator(t Translator) { translator = t }

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// 业务码约定：与 HTTP 状态码保持一致，具体业务可在 10000 以上自定义。
const (
	CodeOK           = 0
	CodeBadRequest   = 400
	CodeUnauthorized = 401
	CodeForbidden    = 403
	CodeNotFound     = 404
	CodeConflict     = 409
	CodeValidation   = 422
	CodeTooMany      = 429
	CodeInternal     = 500
)

func newErr(status, code int, msg string) *Error { return &Error{Status: status, Code: code, Msg: msg} }

func BadRequest(msg string) *Error   { return newErr(http.StatusBadRequest, CodeBadRequest, msg) }
func Unauthorized(msg string) *Error { return newErr(http.StatusUnauthorized, CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return newErr(http.StatusForbidden, CodeForbidden, msg) }
func NotFoundErr(msg string) *Error  { return newErr(http.StatusNotFound, CodeNotFound, msg) }
func Conflict(msg string) *Error     { return newErr(http.StatusConflict, CodeConflict, msg) }
func Validation(msg string) *Error {
	return newErr(http.StatusUnprocessableEntity, CodeValidation, msg)
}
func TooMany(msg string) *Error { return newErr(http.StatusTooManyRequests, CodeTooMany, msg) }

// Internal 内部错误，原因只写日志。
func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal, Msg: "服务器内部错误", Err: err}
}

// OK 成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: CodeOK, Msg: "ok", Data: data})
}

// Fail 错误响应。非 *Error 的错误一律按内部错误处理，避免泄露细节。
func Fail(c *gin.Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}
	if e.Status >= http.StatusInternalServerError {
		logger(c).Error("请求处理失败", zap.String("path", c.Request.URL.Path), zap.Error(err))
	}
	msg := e.Msg
	if translator != nil {
		msg = translator(c, msg, e.Params)
	}
	c.AbortWithStatusJSON(e.Status, Body{Code: e.Code, Msg: msg})
}

// BaseURL 当前请求的站点地址（协议 + 主机），用于未设置站点地址时生成链接（与原项目 url() 一致）。
func BaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

const loggerKey = "gvb.logger"

// SetLogger 把日志实例放入请求上下文（由中间件调用）。
func SetLogger(c *gin.Context, l *zap.Logger) { c.Set(loggerKey, l) }

func logger(c *gin.Context) *zap.Logger {
	if v, ok := c.Get(loggerKey); ok {
		if l, ok := v.(*zap.Logger); ok {
			return l
		}
	}
	return zap.L()
}
