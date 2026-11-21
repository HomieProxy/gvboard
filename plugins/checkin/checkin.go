// Package checkin 示例插件：用户每日签到，随机奖励账户余额。
//
//	GET  {api}/plugin/checkin/status   今日是否已签到
//	GET  {api}/plugin/checkin/widget   仪表盘卡片状态
//	POST {api}/plugin/checkin/checkin  签到
package checkin

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	sdk "gvboard/pkg/plugin"
)

func init() { sdk.Register(&Plugin{}) }

// Plugin 签到插件。
type Plugin struct{}

func (Plugin) Info() sdk.Info {
	return sdk.Info{Code: "checkin", Name: "每日签到", Version: "1.2.0", Author: "Gvboard", Description: "用户每日在仪表盘签到，随机获得余额"}
}

func (Plugin) Fields() []sdk.Field {
	return []sdk.Field{
		{Key: "min", Label: "最少奖励（分）", Type: sdk.FieldNumber, Default: 1, Required: true},
		{Key: "max", Label: "最多奖励（分）", Type: sdk.FieldNumber, Default: 10, Required: true},
	}
}

func (Plugin) Enable(_ context.Context, h sdk.Host) error {
	_, _, err := bounds(h.Config())
	return err
}

func bounds(cfg sdk.Config) (int64, int64, error) {
	lo, hi := cfg.Int("min"), cfg.Int("max")
	if lo < 0 || hi < lo || hi > 100000 {
		return 0, 0, errors.New("奖励范围无效：需要 0 ≤ 最少 ≤ 最多 ≤ 100000")
	}
	return lo, hi, nil
}

func dayKey(uid int64) string {
	return "d:" + time.Now().Format("20060102") + ":" + strconv.FormatInt(uid, 10)
}

// yuan 分转换为元，如 5 → "0.05"。
func yuan(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// todayReward 今日签到奖励（分），未签到时 ok 为 false。
func todayReward(ctx context.Context, h sdk.Host, uid int64) (reward int64, ok bool, err error) {
	v, err := h.KV().Get(ctx, dayKey(uid))
	if errors.Is(err, sdk.ErrKVNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	reward, _ = strconv.ParseInt(v, 10, 64)
	return reward, true, nil
}

func (Plugin) Setup(h sdk.Host) error {
	h.Widget(sdk.Widget{Key: "checkin", Slot: sdk.SlotDashboard, Title: "每日签到", Icon: sdk.IconCalendarCheck, State: "/widget"})
	r := h.UserRoutes()
	r.GET("/status", func(c *gin.Context) {
		_, ok, err := todayReward(c.Request.Context(), h, sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, gin.H{"checked": ok})
	})
	r.GET("/widget", func(c *gin.Context) {
		reward, ok, err := todayReward(c.Request.Context(), h, sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		if ok {
			sdk.OK(c, sdk.WidgetState{Text: "今日已签到，明天再来", Value: "+" + yuan(reward)})
			return
		}
		lo, hi, err := bounds(h.Config())
		if err != nil {
			sdk.OK(c, sdk.WidgetState{Hidden: true})
			return
		}
		text := "签到可获得 " + yuan(lo) + " 余额"
		if hi > lo {
			text = "签到可随机获得 " + yuan(lo) + " ~ " + yuan(hi) + " 余额"
		}
		sdk.OK(c, sdk.WidgetState{Text: text, Actions: []sdk.WidgetAction{{Label: "签到", Path: "/checkin"}}})
	})
	r.POST("/checkin", func(c *gin.Context) {
		ctx, u := c.Request.Context(), sdk.CurrentUser(c)
		lo, hi, err := bounds(h.Config())
		if err != nil {
			sdk.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		reward := lo + rand.Int64N(hi-lo+1)
		key := dayKey(u.ID)
		ok, err := h.KV().SetNX(ctx, key, strconv.FormatInt(reward, 10), 48*time.Hour)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		if !ok {
			sdk.Fail(c, http.StatusBadRequest, "今天已经签到过了")
			return
		}
		if reward > 0 {
			if _, err := h.AddBalance(ctx, u.ID, reward, "每日签到"); err != nil {
				_ = h.KV().Delete(context.WithoutCancel(ctx), key)
				sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
				return
			}
		}
		sdk.OK(c, gin.H{"reward": reward, "message": "签到成功，余额 +" + yuan(reward)})
	})
	return nil
}
