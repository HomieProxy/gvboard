package legacy

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"gvboard/internal/passport"
	"gvboard/internal/subscription"
)

// authData 原项目 AuthService::generateAuthData：token 为订阅 token（取主订阅），auth_data 为登录凭证。
func (m *Module) authData(c *gin.Context, res *passport.AuthResult) (gin.H, error) {
	ctx := c.Request.Context()
	id, err := m.Auth.Parse(ctx, res.Token)
	if err != nil {
		return nil, err
	}
	var token any
	sub, err := m.Subs.Primary(ctx, m.DB, id.ID)
	switch {
	case err == nil:
		token = sub.Token
	case !errors.Is(err, subscription.ErrNotFound):
		return nil, err
	}
	isAdmin := 0
	if res.IsAdmin {
		isAdmin = 1
	}
	return gin.H{"token": token, "is_admin": isAdmin, "auth_data": res.Token}, nil
}

func (m *Module) respondAuth(c *gin.Context, res *passport.AuthResult, err error) {
	if err != nil {
		m.fail(c, err)
		return
	}
	data, err := m.authData(c, res)
	m.respond(c, data, err)
}

// validateEmail email 字段的 required、string 规则，格式由原生服务校验（错误同样按 422 返回）。
func validateEmail(v *validator) {
	if v.required("email", "Email can not be empty") {
		v.check("email", v.in.isString("email"), "Email format is incorrect")
	}
}

func (m *Module) register(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	validateEmail(v)
	v.required("password", "Password can not be empty")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	res, err := m.Passport.Register(c.Request.Context(), passport.RegisterInput{
		Email:      in.str("email"),
		Password:   in.str("password"),
		InviteCode: in.str("invite_code"),
		EmailCode:  in.str("email_code"),
		Captcha:    in.str("recaptcha_data"),
	}, m.client(c))
	m.respondAuth(c, res, err)
}

func (m *Module) login(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	validateEmail(v)
	v.required("password", "Password can not be empty")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	res, err := m.Passport.Login(c.Request.Context(), passport.LoginInput{
		Email: in.str("email"), Password: in.str("password"),
	}, m.client(c))
	m.respondAuth(c, res, err)
}

// token2Login 带 token 参数时跳转到前端登录页（由前端用 verify 换取凭证），带 verify 参数时用快捷登录码登录。
func (m *Module) token2Login(c *gin.Context) {
	in := params(c)
	if in.truthy("token") {
		redirect := in.str("redirect")
		if !in.truthy("redirect") {
			redirect = "dashboard"
		}
		base := m.Settings.String("app_url")
		if base == "" {
			base = baseURL(c)
		}
		c.Redirect(http.StatusFound, base+"/#/login?verify="+url.QueryEscape(in.str("token"))+"&redirect="+url.QueryEscape(redirect))
		return
	}
	if in.truthy("verify") {
		res, err := m.Passport.CodeLogin(c.Request.Context(), in.str("verify"), m.client(c))
		m.respondAuth(c, res, err)
		return
	}
	c.Status(http.StatusOK)
}

func (m *Module) forget(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	validateEmail(v)
	if v.required("password", "Password can not be empty") {
		v.check("password", in.isString("password"), "Password can not be empty")
	}
	if v.required("email_code", "Email verification code cannot be empty") {
		v.check("email_code", in.isString("email_code"), "Incorrect email verification code")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	err := m.Passport.ResetPassword(c.Request.Context(), passport.ResetInput{
		Email: in.str("email"), EmailCode: in.str("email_code"), Password: in.str("password"),
	})
	m.respond(c, true, err)
}

func (m *Module) quickLoginURL(c *gin.Context) {
	in := params(c)
	redirect := ""
	if in.truthy("redirect") {
		redirect = in.str("redirect")
	}
	link, err := m.Passport.QuickLoginURL(c.Request.Context(), uid(c), redirect, baseURL(c))
	m.respond(c, link, err)
}

// sendEmailVerify isforget 为 0 时要求邮箱未注册，为 1 时要求已注册，不传不检查。
func (m *Module) sendEmailVerify(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	validateEmail(v)
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	scene := ""
	if in.has("isforget") && in.raw("isforget") != nil {
		switch in.str("isforget") {
		case "0":
			scene = passport.SceneRegister
		case "1":
			scene = passport.SceneReset
		}
	}
	err := m.Passport.SendEmailCode(c.Request.Context(), passport.EmailCodeInput{
		Email: in.str("email"), Scene: scene, Captcha: in.str("recaptcha_data"),
	}, m.client(c))
	m.respond(c, true, err)
}

func (m *Module) pv(c *gin.Context) {
	m.respond(c, true, m.Passport.InviteVisit(c.Request.Context(), params(c).str("invite_code")))
}

func (m *Module) flag(key string) int {
	if m.Settings.Bool(key) {
		return 1
	}
	return 0
}

// setting 设置原值，空字符串为 null（原项目保存设置时空字符串转为 null）。
func (m *Module) setting(key string) any {
	v := m.Settings.Get(key)
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	return v
}

// guestConfig 原项目 guest/comm/config。未开启邮箱后缀白名单时 email_whitelist_suffix 为 0。
func (m *Module) guestConfig(c *gin.Context) {
	var suffix any = 0
	if m.Settings.Bool("email_whitelist_enable") {
		suffix = m.Settings.Strings("email_whitelist_suffix")
	}
	ok(c, gin.H{
		"tos_url":                m.setting("tos_url"),
		"is_email_verify":        m.flag("email_verify"),
		"is_invite_force":        m.flag("invite_force"),
		"email_whitelist_suffix": suffix,
		"is_recaptcha":           m.flag("recaptcha_enable"),
		"recaptcha_site_key":     m.setting("recaptcha_site_key"),
		"app_description":        m.setting("app_description"),
		"app_url":                m.setting("app_url"),
		"logo":                   m.setting("logo"),
	})
}
