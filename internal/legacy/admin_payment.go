package legacy

import (
	"errors"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/payment"
)

func (m *Module) adminPaymentMethods(c *gin.Context) { ok(c, payment.Names()) }

func (m *Module) adminPaymentFetch(c *gin.Context) {
	list, err := m.Payments.AdminList(c.Request.Context(), m.Payments.SiteURL(c.Request))
	m.respond(c, list, err)
}

func (m *Module) adminPaymentForm(c *gin.Context) {
	in := params(c)
	var id int64
	if in.truthy("id") {
		var valid bool
		if id, valid = in.id(); !valid {
			m.fail(c, abortRaw("支付方式不存在"))
			return
		}
	}
	form, err := m.Payments.AdminForm(c.Request.Context(), in.str("payment"), id)
	m.respond(c, form, err)
}

func (m *Module) adminPaymentShow(c *gin.Context) {
	id, _ := params(c).id()
	m.respond(c, true, m.Payments.Toggle(c.Request.Context(), id))
}

// adminPaymentSave 新增或修改支付方式。修改时未出现在请求中的可选字段保留原值（原项目 $payment->update($params)）。
func (m *Module) adminPaymentSave(c *gin.Context) {
	if m.Settings.String("app_url") == "" {
		m.fail(c, abortRaw("请在站点配置中配置站点地址"))
		return
	}
	in := params(c)
	v := newValidator(in)
	v.required("name", "显示名称不能为空")
	v.required("payment", "网关参数不能为空")
	v.required("config", "配置参数不能为空")
	if v.present("notify_domain") {
		u, err := url.Parse(in.str("notify_domain"))
		v.check("notify_domain", err == nil && u.Scheme != "" && u.Host != "", "自定义通知域名格式有误")
	}
	v.integer("handling_fee_fixed", "固定手续费格式有误")
	if v.present("handling_fee_percent") {
		f, isNum := in.float("handling_fee_percent")
		if v.check("handling_fee_percent", isNum, "validation.numeric") {
			v.check("handling_fee_percent", f >= 0.1 && f <= 100, "百分比手续费范围须在0.1-100之间")
		}
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	cfg, isObj := in.raw("config").(map[string]any)
	if !isObj {
		if list, isList := in.raw("config").([]any); isList {
			cfg = make(map[string]any, len(list))
			for i, x := range list {
				cfg[strconv.Itoa(i)] = x
			}
		} else {
			m.fail(c, abortRaw("配置参数不能为空"))
			return
		}
	}

	var cur model.Payment
	s := payment.SaveInput{Name: in.str("name"), Payment: in.str("payment"), Config: cfg}
	if in.truthy("id") {
		id, _ := in.id()
		err := m.DB.WithContext(c.Request.Context()).Take(&cur, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
			m.fail(c, abortRaw("支付方式不存在"))
			return
		} else if err != nil {
			m.fail(c, err)
			return
		}
		s.ID = cur.ID
	}
	s.Icon = in.optStr("icon", cur.Icon)
	s.NotifyDomain = in.optStr("notify_domain", cur.NotifyDomain)
	s.HandlingFeeFixed = in.optInt("handling_fee_fixed", cur.HandlingFeeFixed)
	s.HandlingFeePercent = cur.HandlingFeePercent
	if in.has("handling_fee_percent") {
		s.HandlingFeePercent = nil
		if f, isNum := in.float("handling_fee_percent"); isNum {
			s.HandlingFeePercent = &f
		}
	}
	m.respond(c, true, m.Payments.Save(c.Request.Context(), s))
}

func (m *Module) adminPaymentDrop(c *gin.Context) {
	id, _ := params(c).id()
	m.respond(c, true, m.Payments.Delete(c.Request.Context(), id))
}

func (m *Module) adminPaymentSort(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if v.required("ids", "参数有误") {
		v.check("ids", in.isArray("ids"), "参数有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	ids, valid := in.intList("ids")
	if !valid {
		m.fail(c, abortRaw("保存失败"))
		return
	}
	m.respond(c, true, fromNative(m.Payments.Sort(c.Request.Context(), ids), "保存失败"))
}
