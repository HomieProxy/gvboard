package legacy

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
)

const (
	// msgLoginRequired 原项目认证失败的固定文案（未翻译）。
	msgLoginRequired = "未登录或登陆已过期"
	// msgInternal 原项目非调试模式下的内部错误文案。
	msgInternal = "Uh-oh, we've had some problems, we're working on it."
	// msgInvalidData 原项目表单校验失败的固定文案。
	msgInvalidData = "The given data was invalid."
	// msgTooMany 原项目唯一返回 429 的错误（发送邮箱验证码过于频繁）。
	msgTooMany = "Too many requests, please try again later."
)

// abortError 对应原项目 abort(status, message)。key 为翻译键，raw 为 true 时原样输出。
type abortError struct {
	status int
	key    string
	params map[string]any
	raw    bool
}

func (e *abortError) Error() string { return e.key }

// abort 原项目最常见的 abort(500, __(key))。
func abort(key string) error { return &abortError{status: http.StatusInternalServerError, key: key} }

func abortWith(key string, params map[string]any) error {
	return &abortError{status: http.StatusInternalServerError, key: key, params: params}
}

// fieldError 表单校验失败的一项。
type fieldError struct {
	field string
	key   string
}

// invalidError 表单校验失败（原项目 FormRequest），返回 422 与各字段的错误信息。
type invalidError struct{ fields []fieldError }

func (e *invalidError) Error() string { return msgInvalidData }

// validationFields 原生服务内部格式校验的错误文案对应的字段。原项目这些检查属于 FormRequest，失败时返回 422。
var validationFields = map[string]string{
	"Email can not be empty":                  "email",
	"Email format is incorrect":               "email",
	"Password can not be empty":               "password",
	"Password must be greater than 8 digits":  "password",
	"Email verification code cannot be empty": "email_code",
}

// validator 按字段收集校验错误，每个字段只记录第一条（与 Laravel 遇到首个失败规则后的输出一致）。
type validator struct {
	in     input
	failed map[string]bool
	errs   []fieldError
}

func newValidator(in input) *validator { return &validator{in: in, failed: map[string]bool{}} }

// check 字段尚未失败且 ok 为 false 时记录错误，返回字段当前是否通过。
func (v *validator) check(field string, ok bool, key string) bool {
	if v.failed[field] {
		return false
	}
	if !ok {
		v.failed[field] = true
		v.errs = append(v.errs, fieldError{field: field, key: key})
	}
	return ok
}

// required 对应 required 规则：参数存在且不为 null（空字符串已转为 null）。
func (v *validator) required(field, key string) bool {
	val := v.in.raw(field)
	ok := val != nil
	switch x := val.(type) {
	case []any:
		ok = len(x) > 0
	case map[string]any:
		ok = len(x) > 0
	}
	return v.check(field, ok, key)
}

// present 字段有值（非 null）时才执行后续规则，与 Laravel 对可选字段的处理一致。
func (v *validator) present(field string) bool {
	return v.in.raw(field) != nil && !v.failed[field]
}

func (v *validator) err() error {
	if len(v.errs) == 0 {
		return nil
	}
	return &invalidError{fields: v.errs}
}

func (m *Module) tr(c *gin.Context, key string, params map[string]any) string {
	return m.I18n.Tr(c, key, i18n.Params(params))
}

// ok 原项目 response(['data' => ...])。
func ok(c *gin.Context, data any) { c.JSON(http.StatusOK, gin.H{"data": data}) }

// respond 出错时按原项目格式返回错误，否则返回 data。
func (m *Module) respond(c *gin.Context, data any, err error) {
	if err != nil {
		m.fail(c, err)
		return
	}
	ok(c, data)
}

// fail 把错误转换为原项目的响应：业务错误 500 {"message"}，表单校验 422，内部错误只返回通用文案。
func (m *Module) fail(c *gin.Context, err error) {
	var (
		ie *invalidError
		ae *abortError
		he *httpx.Error
	)
	switch {
	case errors.As(err, &ie):
		m.invalid(c, ie.fields)
	case errors.As(err, &ae):
		msg := ae.key
		if !ae.raw {
			msg = m.tr(c, ae.key, ae.params)
		}
		c.AbortWithStatusJSON(ae.status, gin.H{"message": msg})
	case errors.As(err, &he):
		switch {
		case he.Status >= http.StatusInternalServerError:
			m.internal(c, err)
		case he.Status == http.StatusUnprocessableEntity && validationFields[he.Msg] != "":
			m.invalid(c, []fieldError{{field: validationFields[he.Msg], key: he.Msg}})
		case he.Status == http.StatusTooManyRequests && he.Msg == msgTooMany:
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"message": m.tr(c, he.Msg, he.Params)})
		default:
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": m.tr(c, he.Msg, he.Params)})
		}
	default:
		m.internal(c, err)
	}
}

func (m *Module) invalid(c *gin.Context, fields []fieldError) {
	errs := make(map[string][]string, len(fields))
	for _, f := range fields {
		errs[f.field] = append(errs[f.field], m.tr(c, f.key, nil))
	}
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"message": msgInvalidData, "errors": errs})
}

func (m *Module) internal(c *gin.Context, err error) {
	m.log().Error("兼容层请求处理失败", zap.String("path", c.Request.URL.Path), zap.Error(err))
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": m.tr(c, msgInternal, nil)})
}

// unauthorized 原项目 abort(403, '未登录或登陆已过期')。
func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": msgLoginRequired})
}
