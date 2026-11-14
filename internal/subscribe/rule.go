package subscribe

import (
	"context"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
)

// 以下为管理端规则管理（接口在单元 14 挂载）。规则修改后各实例最迟 5 秒内生效。

var errRuleNotExist = httpx.NotFoundErr("规则不存在")

func (in RuleInput) toModel() model.SubscribeRule {
	r := model.SubscribeRule{
		Name:      strings.TrimSpace(in.Name),
		Target:    in.Target,
		Header:    strings.TrimSpace(in.Header),
		MatchType: in.MatchType,
		Action:    in.Action,
		Priority:  in.Priority,
	}
	if in.Pattern != "" {
		p := in.Pattern
		r.Pattern = &p
	}
	if in.Negate {
		r.Negate = 1
	}
	if in.Enabled {
		r.Enabled = 1
	}
	return r
}

// Rules 全部规则，按优先级与 ID 排序（与匹配顺序一致）。
func (s *Service) Rules(ctx context.Context) ([]model.SubscribeRule, error) {
	out := []model.SubscribeRule{}
	err := s.DB.WithContext(ctx).Order("priority ASC, id ASC").Find(&out).Error
	return out, err
}

// SaveRule 新建（id 为 0）或修改规则。修改不会重置命中数。
func (s *Service) SaveRule(ctx context.Context, id int64, in RuleInput) (*model.SubscribeRule, error) {
	c, err := compileRule(in.toModel())
	if err != nil {
		return nil, err
	}
	r := c.SubscribeRule
	db := s.DB.WithContext(ctx)
	if id == 0 {
		if err := db.Create(&r).Error; err != nil {
			return nil, err
		}
	} else {
		var cur model.SubscribeRule
		if err := db.Where("id = ?", id).Take(&cur).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errRuleNotExist
		} else if err != nil {
			return nil, err
		}
		err := db.Model(&cur).Select("name", "target", "header", "match_type", "pattern", "negate", "action", "priority", "enabled", "updated_at").
			Updates(&r).Error
		if err != nil {
			return nil, err
		}
		if err := db.Where("id = ?", id).Take(&r).Error; err != nil {
			return nil, err
		}
	}
	s.invalidateRules(ctx)
	return &r, nil
}

// DeleteRule 删除规则。日志中的 rule_id 保留，便于追溯。
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.SubscribeRule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errRuleNotExist
	}
	s.invalidateRules(ctx)
	return nil
}

// TestRule 用示例请求测试规则是否命中（管理端编辑规则时预览）。client 为空时按 flag（或 User-Agent）识别客户端格式。
func (s *Service) TestRule(in RuleInput, a Access, client string) (bool, error) {
	c, err := compileRule(in.toModel())
	if err != nil {
		return false, err
	}
	if client == "" {
		flag := a.Flag
		if flag == "" {
			flag = a.UserAgent
		}
		p, _ := selectProtocol(s.protocols(), strings.ToLower(flag))
		client = p.Name
	}
	return c.match(a, client), nil
}

// ClientNames 可用于"客户端格式"规则的名称列表。
func (s *Service) ClientNames() []string {
	names := []string{}
	seen := map[string]bool{}
	for _, p := range slices.Concat(s.protocols(), []*Protocol{protoSingbox, protoSingboxOld, protoGeneral}) {
		if !seen[p.Name] {
			seen[p.Name] = true
			names = append(names, p.Name)
		}
	}
	return append(names, clientApp)
}
