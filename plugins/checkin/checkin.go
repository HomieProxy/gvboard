// Package checkin 示例插件：用户每日签到，随机奖励账户余额。
//
//	GET  {api}/plugin/checkin/status   今日是否已签到
//	POST {api}/plugin/checkin/checkin  签到
package checkin

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	sdk "gvboard/pkg/plugin"
)

func init() { sdk.Register(&Plugin{}) }

// Plugin 签到插件。
type Plugin struct{}

func (Plugin) Info() sdk.Info {
	return sdk.Info{Code: "checkin", Name: "每日签到", Version: "1.0.0", Author: "Gvboard", Description: "用户每日签到随机获得余额"}
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

func (Plugin) Setup(h sdk.Host) error {
	r := h.UserRoutes()
	r.GET("/status", func(c *gin.Context) {
		u := sdk.CurrentUser(c)
		_, err := h.KV().Get(c.Request.Context(), dayKey(u.ID))
		if err != nil && !errors.Is(err, sdk.ErrKVNotFound) {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, gin.H{"checked": err == nil})
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
		if err := h.DB().WithContext(ctx).Table("v2_user").Where("id = ?", u.ID).
			Update("balance", gorm.Expr("balance + ?", reward)).Error; err != nil {
			_ = h.KV().Delete(context.WithoutCancel(ctx), key)
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, gin.H{"reward": reward})
	})
	return nil
}
