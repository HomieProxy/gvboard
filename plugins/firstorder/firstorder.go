// Package firstorder 示例插件：新用户首单优惠。用户第一次购买套餐（新购）时，按比例减免订单金额。
// 演示过滤器（修改下单金额并附带明细、为套餐添加标签）、仪表盘卡片、读取数据库、配置校验与用户接口。
//
//	GET {api}/plugin/first_order/eligible  当前用户能否享受首单优惠
//	GET {api}/plugin/first_order/widget    仪表盘卡片状态（不能享受时隐藏）
package firstorder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	sdk "gvboard/pkg/plugin"
)

func init() { sdk.Register(&Plugin{}) }

const (
	orderTypeNew     = 1 // 新购
	orderTypeDeposit = 9 // 充值
)

// 开通中、已完成、已折抵的订单视为已购买过。
var paidStatuses = []int{1, 3, 4}

// Plugin 首单优惠插件。
type Plugin struct{}

func (Plugin) Info() sdk.Info {
	return sdk.Info{
		Code: "first_order", Name: "首单优惠", Version: "1.1.0", Author: "Gvboard",
		Description: "从未购买过套餐的用户新购套餐时按比例减免金额（在优惠券之后计算），仪表盘显示首单优惠卡片，套餐显示“首单立减”标签",
	}
}

func (Plugin) Fields() []sdk.Field {
	return []sdk.Field{
		{Key: "percent", Label: "减免比例（%）", Type: sdk.FieldNumber, Default: 20, Required: true, Help: "1～100，100 表示首单免费"},
		{Key: "max_off", Label: "最多减免（分）", Type: sdk.FieldNumber, Default: 0, Help: "0 表示不限制"},
		{Key: "plan_ids", Label: "适用套餐 ID", Type: sdk.FieldString, Help: "英文逗号分隔，留空表示全部套餐"},
	}
}

// rule 首单优惠规则。
type rule struct {
	percent int64
	maxOff  int64
	plans   []int64
}

func parseRule(cfg sdk.Config) (rule, error) {
	r := rule{percent: cfg.Int("percent"), maxOff: cfg.Int("max_off")}
	if r.percent < 1 || r.percent > 100 {
		return r, errors.New("减免比例必须为 1～100")
	}
	if r.maxOff < 0 {
		return r, errors.New("最多减免不能为负数")
	}
	for s := range strings.SplitSeq(cfg.String("plan_ids"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return r, errors.New("适用套餐 ID 格式错误：" + s)
		}
		r.plans = append(r.plans, id)
	}
	return r, nil
}

func (r rule) applies(planID int64) bool {
	return len(r.plans) == 0 || slices.Contains(r.plans, planID)
}

// badge 套餐标签。
func (r rule) badge() string {
	if r.percent == 100 {
		return "首单免费"
	}
	return "首单立减 " + strconv.FormatInt(r.percent, 10) + "%"
}

// widget 仪表盘卡片状态。
func (r rule) widget() sdk.WidgetState {
	text := "首次购买套餐立减 " + strconv.FormatInt(r.percent, 10) + "%"
	if r.percent == 100 {
		text = "首次购买套餐免费"
	}
	if r.maxOff > 0 {
		text += "，最多减免 " + fmt.Sprintf("%d.%02d", r.maxOff/100, r.maxOff%100)
	}
	if len(r.plans) > 0 {
		text += "（限指定套餐）"
	}
	text += "，下单时自动抵扣"
	value := "-" + strconv.FormatInt(r.percent, 10) + "%"
	if r.percent == 100 {
		value = "免费"
	}
	return sdk.WidgetState{Text: text, Value: value, Actions: []sdk.WidgetAction{{Label: "去选套餐", Link: "/plans"}}}
}

// discount 减免金额（分），向下取整且不超过上限与订单金额。
func (r rule) discount(amount int64) int64 {
	off := amount * r.percent / 100
	if r.maxOff > 0 && off > r.maxOff {
		off = r.maxOff
	}
	return min(off, amount)
}

func (Plugin) Enable(_ context.Context, h sdk.Host) error {
	_, err := parseRule(h.Config())
	return err
}

// purchased 用户是否已购买过套餐（充值不算）。
func purchased(ctx context.Context, db *gorm.DB, userID int64) (bool, error) {
	var n int64
	err := db.WithContext(ctx).Table("v2_order").
		Where("user_id = ? AND status IN ? AND type <> ?", userID, paidStatuses, orderTypeDeposit).
		Limit(1).Count(&n).Error
	return n > 0, err
}

func (Plugin) Setup(h sdk.Host) error {
	h.Widget(sdk.Widget{Key: "first_order", Slot: sdk.SlotDashboard, Title: "首单优惠", Icon: sdk.IconSparkles, State: "/widget"})
	sdk.AddFilter(h, sdk.FilterOrderAmount, 100, func(ctx context.Context, v sdk.OrderAmount) (sdk.OrderAmount, error) {
		if v.Type != orderTypeNew || v.Amount <= 0 {
			return v, nil
		}
		r, err := parseRule(h.Config())
		if err != nil || !r.applies(v.PlanID) {
			return v, nil
		}
		done, err := purchased(ctx, h.DB(), v.UserID)
		if err != nil {
			return v, err
		}
		if off := r.discount(v.Amount); !done && off > 0 {
			v.Amount -= off
			v.Items = append(v.Items, sdk.AmountItem{Label: "首单优惠", Amount: -off})
		}
		return v, nil
	})

	sdk.AddFilter(h, sdk.FilterPlanBadges, 100, func(ctx context.Context, v sdk.PlanBadges) (sdk.PlanBadges, error) {
		r, err := parseRule(h.Config())
		if err != nil {
			return v, nil
		}
		done, err := purchased(ctx, h.DB(), v.UserID)
		if err != nil || done {
			return v, err
		}
		for i := range v.Plans {
			if r.applies(v.Plans[i].PlanID) {
				v.Plans[i].Badges = append(v.Plans[i].Badges, r.badge())
			}
		}
		return v, nil
	})

	h.UserRoutes().GET("/eligible", func(c *gin.Context) {
		r, err := parseRule(h.Config())
		if err != nil {
			sdk.OK(c, gin.H{"eligible": false})
			return
		}
		done, err := purchased(c.Request.Context(), h.DB(), sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, gin.H{"eligible": !done, "percent": r.percent, "max_off": r.maxOff, "plan_ids": r.plans})
	})

	h.UserRoutes().GET("/widget", func(c *gin.Context) {
		r, err := parseRule(h.Config())
		if err != nil {
			sdk.OK(c, sdk.WidgetState{Hidden: true})
			return
		}
		done, err := purchased(c.Request.Context(), h.DB(), sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		if done {
			sdk.OK(c, sdk.WidgetState{Hidden: true})
			return
		}
		sdk.OK(c, r.widget())
	})
	return nil
}
