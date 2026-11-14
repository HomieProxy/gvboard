package usercenter

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

var (
	errGiftcardInvalid  = errInvalidParams
	errGiftcardNone     = httpx.NotFoundErr("The gift card does not exist")
	errGiftcardDup      = httpx.Conflict("The gift card code already exists")
	errGiftcardPlanNone = errPlanNotExist
	errGiftcardCount    = httpx.Validation("The number of gift cards generated at a time is 1 to 500")
)

// GiftcardPage 管理端礼品卡分页参数。
type GiftcardPage struct {
	Current  int    `form:"current"`
	PageSize int    `form:"page_size"`
	Sort     string `form:"sort"`
	SortType string `form:"sort_type"`
}

// GiftcardList 礼品卡分页结果。
type GiftcardList struct {
	Data  []model.Giftcard `json:"data"`
	Total int64            `json:"total"`
}

var giftcardSortable = []string{"id", "code", "name", "type", "value", "plan_id", "limit_use", "started_at", "ended_at", "created_at", "updated_at"}

// AdminGiftcards 礼品卡列表（对应原项目 admin/giftcard/fetch），排序字段只允许白名单内的列。
func (s *Service) AdminGiftcards(ctx context.Context, p GiftcardPage) (*GiftcardList, error) {
	sort := p.Sort
	if !slices.Contains(giftcardSortable, sort) {
		sort = "id"
	}
	size := min(max(p.PageSize, 10), 100)
	cur := max(p.Current, 1)
	db := s.DB.WithContext(ctx)
	out := &GiftcardList{Data: []model.Giftcard{}}
	if err := db.Model(&model.Giftcard{}).Count(&out.Total).Error; err != nil {
		return nil, err
	}
	err := db.Order(clause.OrderByColumn{Column: clause.Column{Name: sort}, Desc: !strings.EqualFold(p.SortType, "ASC")}).
		Offset((cur - 1) * size).Limit(size).Find(&out.Data).Error
	return out, err
}

// GiftcardInput 礼品卡保存参数（与原项目 GiftcardGenerate 规则一致）。
// value：余额类为分，时长与套餐类为天数，流量类为 GB；重置流量类不需要。plan_id 只用于套餐类。
// started_at、ended_at 为 0 表示不限制。
type GiftcardInput struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	Value     *int64 `json:"value"`
	PlanID    *int64 `json:"plan_id"`
	LimitUse  *int64 `json:"limit_use"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
}

func (in *GiftcardInput) validate(tx *gorm.DB) error {
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 255 || len(in.Code) > 255 || in.Type < GiftcardBalance || in.Type > GiftcardPlan {
		return errGiftcardInvalid
	}
	if in.StartedAt < 0 || in.EndedAt < 0 || (in.StartedAt > 0 && in.EndedAt > 0 && in.EndedAt < in.StartedAt) {
		return errGiftcardInvalid
	}
	if in.LimitUse != nil && *in.LimitUse < 0 {
		return errGiftcardInvalid
	}
	if in.Type != GiftcardResetTraffic && (in.Value == nil || *in.Value < 0) {
		return errGiftcardInvalid
	}
	if in.Type != GiftcardPlan {
		in.PlanID = nil
		return nil
	}
	if in.PlanID == nil {
		return errGiftcardInvalid
	}
	var n int64
	if err := tx.Model(&model.Plan{}).Where("id = ?", *in.PlanID).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errGiftcardPlanNone
	}
	return nil
}

func (in *GiftcardInput) model() model.Giftcard {
	return model.Giftcard{Code: in.Code, Name: in.Name, Type: in.Type, Value: in.Value, PlanID: in.PlanID,
		LimitUse: in.LimitUse, StartedAt: in.StartedAt, EndedAt: in.EndedAt}
}

// SaveGiftcard 新建（ID 为 0）或修改礼品卡（对应原项目 admin/giftcard/generate 单个生成）。
// 新建时未填卡密则随机生成 16 位；卡密不能与其他礼品卡重复（原项目未检查，重复时只有第一张能兑换）。
func (s *Service) SaveGiftcard(ctx context.Context, in GiftcardInput) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := in.validate(tx); err != nil {
			return err
		}
		if in.ID != 0 {
			var old model.Giftcard
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&old, in.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return errGiftcardNone
			} else if err != nil {
				return err
			}
			if in.Code == "" {
				in.Code = old.Code
			}
		} else if in.Code == "" {
			in.Code = util.RandomChar(16, false)
		}
		var n int64
		if err := tx.Model(&model.Giftcard{}).Where("code = ? AND id <> ?", in.Code, in.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errGiftcardDup
		}
		g := in.model()
		if in.ID == 0 {
			return tx.Create(&g).Error
		}
		g.ID = in.ID
		return tx.Model(&g).Select("code", "name", "type", "value", "plan_id", "limit_use", "started_at", "ended_at").Updates(&g).Error
	})
}

// GenerateGiftcards 批量生成礼品卡（最多 500 张），返回 CSV（对应原项目 admin/giftcard/generate 批量生成）。
func (s *Service) GenerateGiftcards(ctx context.Context, in GiftcardInput, count int) ([]byte, error) {
	if count < 1 || count > 500 {
		return nil, errGiftcardCount
	}
	var rows []model.Giftcard
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := in.validate(tx); err != nil {
			return err
		}
		seen := map[string]bool{}
		for len(rows) < count {
			code := util.RandomChar(16, false)
			if seen[code] {
				continue
			}
			var n int64
			if err := tx.Model(&model.Giftcard{}).Where("code = ?", code).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			seen[code] = true
			g := in.model()
			g.Code = code
			rows = append(rows, g)
		}
		return tx.CreateInBatches(rows, 100).Error
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	_ = w.Write([]string{"名称", "类型", "数值", "开始时间", "结束时间", "可用次数", "礼品卡卡密", "生成时间"})
	const layout = "2006-01-02 15:04:05"
	value := int64(0)
	if in.Value != nil {
		value = *in.Value
	}
	typ := map[int]string{GiftcardBalance: "金额", GiftcardDuration: "时长", GiftcardTraffic: "流量", GiftcardResetTraffic: "重置", GiftcardPlan: "套餐"}[in.Type]
	val := map[int]string{
		GiftcardBalance: strconv.FormatFloat(float64(value)/100, 'f', -1, 64), GiftcardDuration: strconv.FormatInt(value, 10) + "天",
		GiftcardTraffic: strconv.FormatInt(value, 10) + "GB", GiftcardResetTraffic: "-", GiftcardPlan: strconv.FormatInt(value, 10) + "天",
	}[in.Type]
	timeText := func(t int64) string {
		if t == 0 {
			return "不限制"
		}
		return time.Unix(t, 0).Format(layout)
	}
	limit := "不限制"
	if in.LimitUse != nil {
		limit = strconv.FormatInt(*in.LimitUse, 10)
	}
	name := in.Name
	if name != "" && strings.ContainsRune("=+-@\t\r", rune(name[0])) {
		name = "'" + name
	}
	for _, g := range rows {
		_ = w.Write([]string{name, typ, val, timeText(g.StartedAt), timeText(g.EndedAt), limit, g.Code, time.Unix(g.CreatedAt, 0).Format(layout)})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// DeleteGiftcard 删除礼品卡（对应原项目 admin/giftcard/drop）。
func (s *Service) DeleteGiftcard(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Delete(&model.Giftcard{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errGiftcardNone
	}
	return nil
}
