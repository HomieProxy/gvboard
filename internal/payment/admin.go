package payment

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

// 管理端错误。管理端只有中文界面，与原项目一样直接使用中文文案。
var (
	errAdminInvalid    = httpx.Validation("参数有误")
	errAdminNone       = httpx.NotFoundErr("支付方式不存在")
	errAdminGateway    = httpx.BadRequest("gate is not found")
	errAdminAppURL     = httpx.BadRequest("请在站点配置中配置站点地址")
	errAdminName       = httpx.Validation("显示名称不能为空")
	errAdminPayment    = httpx.Validation("网关参数不能为空")
	errAdminConfig     = httpx.Validation("配置参数不能为空")
	errAdminDomain     = httpx.Validation("自定义通知域名格式有误")
	errAdminFeePercent = httpx.Validation("百分比手续费范围须在0.1-100之间")
)

// AdminPayment 管理端支付方式（含回调地址）。
type AdminPayment struct {
	model.Payment
	NotifyURL string `json:"notify_url"`
}

// AdminList 全部支付方式（原项目 payment/fetch），site 为当前请求的站点地址。
func (s *Service) AdminList(ctx context.Context, site string) ([]AdminPayment, error) {
	var ps []model.Payment
	if err := bySort(s.DB.WithContext(ctx)).Find(&ps).Error; err != nil {
		return nil, err
	}
	out := make([]AdminPayment, len(ps))
	for i := range ps {
		out[i] = AdminPayment{Payment: ps[i], NotifyURL: s.NotifyURL(site, &ps[i])}
	}
	return out, nil
}

// AdminForm 网关配置表单，id 不为 0 时填入该支付方式的现有配置（原项目 payment/getPaymentForm）。
func (s *Service) AdminForm(ctx context.Context, gateway string, id int64) (Form, error) {
	g, ok := Lookup(gateway)
	if !ok {
		return nil, errAdminGateway
	}
	form := g.Form()
	if id == 0 {
		return form, nil
	}
	p, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg := loadConfig(p.Config)
	for i := range form {
		if v := cfg[form[i].Key]; v != nil {
			form[i].Value = v
		}
	}
	return form, nil
}

func (s *Service) find(ctx context.Context, id int64) (*model.Payment, error) {
	var p model.Payment
	err := s.DB.WithContext(ctx).Take(&p, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errAdminNone
	}
	return &p, err
}

// Toggle 启用 / 停用（原项目 payment/show）。
func (s *Service) Toggle(ctx context.Context, id int64) error {
	p, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	enable := 1
	if p.Enable != 0 {
		enable = 0
	}
	return s.DB.WithContext(ctx).Model(&model.Payment{}).Where("id = ?", id).Update("enable", enable).Error
}

// SaveInput 保存支付方式。
type SaveInput struct {
	ID                 int64          `json:"-"`
	Name               string         `json:"name"`
	Icon               *string        `json:"icon"`
	Payment            string         `json:"payment"`
	Config             map[string]any `json:"config"`
	NotifyDomain       *string        `json:"notify_domain"`
	HandlingFeeFixed   *int64         `json:"handling_fee_fixed"`
	HandlingFeePercent *float64       `json:"handling_fee_percent"`
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := phpTrim(*p)
	if v == "" {
		return nil
	}
	return &v
}

// Save 新增或修改支付方式（原项目 payment/save）。字符串去除首尾空白、空串视为未填（与原项目中间件一致）。
func (s *Service) Save(ctx context.Context, in SaveInput) error {
	if s.Settings.String("app_url") == "" {
		return errAdminAppURL
	}
	in.Name, in.Payment = phpTrim(in.Name), phpTrim(in.Payment)
	in.Icon, in.NotifyDomain = trimPtr(in.Icon), trimPtr(in.NotifyDomain)
	for k, v := range in.Config {
		in.Config[k] = normalize(k, v)
	}
	switch {
	case in.Name == "":
		return errAdminName
	case in.Payment == "":
		return errAdminPayment
	case len(in.Config) == 0:
		return errAdminConfig
	case in.NotifyDomain != nil && !validURL(*in.NotifyDomain):
		return errAdminDomain
	case in.HandlingFeePercent != nil && (*in.HandlingFeePercent < 0.1 || *in.HandlingFeePercent > 100):
		return errAdminFeePercent
	}
	if _, ok := Lookup(in.Payment); !ok {
		return errAdminGateway
	}
	cfg, err := json.Marshal(in.Config)
	if err != nil {
		return errAdminInvalid
	}
	db := s.DB.WithContext(ctx)
	if in.ID != 0 {
		if _, err := s.find(ctx, in.ID); err != nil {
			return err
		}
		return db.Model(&model.Payment{}).Where("id = ?", in.ID).Updates(map[string]any{
			"name":                 in.Name,
			"icon":                 in.Icon,
			"payment":              in.Payment,
			"config":               string(cfg),
			"notify_domain":        in.NotifyDomain,
			"handling_fee_fixed":   in.HandlingFeeFixed,
			"handling_fee_percent": in.HandlingFeePercent,
		}).Error
	}
	p := &model.Payment{
		UUID:               util.RandomChar(8, false),
		Payment:            in.Payment,
		Name:               in.Name,
		Icon:               in.Icon,
		Config:             model.JSON(cfg),
		NotifyDomain:       in.NotifyDomain,
		HandlingFeeFixed:   in.HandlingFeeFixed,
		HandlingFeePercent: in.HandlingFeePercent,
		Enable:             0,
	}
	if err := db.Create(p).Error; err != nil {
		return err
	}
	return nil
}

// Delete 删除支付方式（原项目 payment/drop）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Delete(&model.Payment{}, "id = ?", id).Error
}

// Sort 按给定顺序设置排序值 1..n（原项目 payment/sort）。
func (s *Service) Sort(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return errAdminInvalid
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.Payment{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
			return err
		}
		if int(n) != len(unique(ids)) {
			return errAdminNone
		}
		for i, id := range ids {
			if err := tx.Model(&model.Payment{}).Where("id = ?", id).Update("sort", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func unique(ids []int64) map[int64]struct{} {
	m := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}
