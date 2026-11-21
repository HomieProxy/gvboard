// Package luckywheel 内置插件「幸运大转盘」（决策 D87）：用户每日免费或付费（余额 / 流量）抽奖，
// 奖品为余额、订阅时长、本周期流量或谢谢惠顾，由管理员在管理端「幸运转盘」页面配置。
//
//	GET  {api}/plugin/lucky_wheel/widget    仪表盘卡片状态
//	GET  {api}/plugin/lucky_wheel/page      用户端页面内容（未适配转盘界面的主题使用）
//	GET  {api}/plugin/lucky_wheel/info      转盘信息（奖品、今日剩余次数、付费方式）
//	POST {api}/plugin/lucky_wheel/draw      抽奖 {subscription_id, pay: balance|traffic}
//	GET  {api}/plugin/lucky_wheel/records   我的抽奖记录
//	GET  {api}/plugin/lucky_wheel/traffic-costs  本月消耗流量抽奖的记录（流量明细页使用）
//	GET  {api}/{管理端}/plugin/lucky_wheel/settings   读取配置
//	POST {api}/{管理端}/plugin/lucky_wheel/settings   保存配置
//	GET  {api}/{管理端}/plugin/lucky_wheel/records    全部抽奖记录
package luckywheel

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/model"
	sdk "gvboard/pkg/plugin"
)

// Code 插件标识。
const Code = "lucky_wheel"

// PagePath 用户端转盘页面地址。
const PagePath = "/plugin/" + Code + "/wheel"

// Cooldown 同一用户两次抽奖的最小间隔，0 表示不限制。
var Cooldown = 3 * time.Second

func init() { sdk.Register(&Plugin{}) }

// Plugin 幸运大转盘插件。
type Plugin struct{}

func (Plugin) Info() sdk.Info {
	return sdk.Info{Code: Code, Name: "幸运大转盘", Version: "1.0.0", Author: "Gvboard",
		Description: "用户端导航与仪表盘卡片进入转盘抽奖，奖品为余额、订阅时长、本周期流量；奖品与概率在管理端「幸运转盘」页面配置"}
}

func (Plugin) Setup(h sdk.Host) error {
	h.Widget(sdk.Widget{Key: "wheel", Slot: sdk.SlotDashboard, Title: "幸运大转盘", Icon: sdk.IconGift, State: "/widget"})
	h.Page(sdk.Page{Key: "wheel", Title: "幸运大转盘", Icon: sdk.IconFerrisWheel, Group: sdk.NavMore, Content: "/page", Titles: map[string]string{
		"zh-TW": "幸運大轉盤", "en-US": "Lucky Wheel", "ja-JP": "ラッキールーレット", "ko-KR": "행운의 룰렛", "vi-VN": "Vòng quay may mắn", "fa-IR": "گردونه شانس",
	}})
	h.Job("clean", "40 3 * * *", func(ctx context.Context) error { return clean(ctx, h) })

	r := h.UserRoutes()
	r.GET("/widget", func(c *gin.Context) {
		ctx, uid := c.Request.Context(), sdk.CurrentUser(c).ID
		s, err := load(ctx, h)
		if err != nil {
			sdk.OK(c, sdk.WidgetState{Hidden: true})
			return
		}
		free, _, err := todayCounts(ctx, h, uid)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		text := "免费次数已用完"
		if left := s.FreeDaily - free; left > 0 {
			text = "今日剩余免费抽奖 " + strconv.FormatInt(left, 10) + " 次"
		} else if s.BalanceEnable || s.TrafficEnable {
			text = "今日免费次数已用完，可消耗余额或流量继续抽奖"
		} else if s.FreeDaily > 0 {
			text = "今日免费次数已用完，明天再来"
		}
		sdk.OK(c, sdk.WidgetState{Text: text, Actions: []sdk.WidgetAction{{Label: "去参加", Link: PagePath}}})
	})
	// 未适配转盘界面的主题按区块显示活动说明
	r.GET("/page", func(c *gin.Context) {
		ctx := c.Request.Context()
		s, err := load(ctx, h)
		if err != nil {
			sdk.OK(c, sdk.PageContent{Blocks: []sdk.Block{{Type: sdk.BlockAlert, Tone: sdk.ToneWarning, Text: "活动暂未开放"}}})
			return
		}
		info, err := userInfo(ctx, h, s, sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		rows := make([][]string, 0, len(s.Prizes))
		for _, p := range s.Prizes {
			rows = append(rows, []string{p.Name, p.describe()})
		}
		sdk.OK(c, sdk.PageContent{Blocks: []sdk.Block{
			{Type: sdk.BlockAlert, Tone: sdk.ToneInfo, Text: "当前主题暂不支持转盘抽奖界面，请切换到默认主题参与活动。"},
			{Type: sdk.BlockFields, Fields: []sdk.PageField{{Label: "今日剩余免费次数", Value: strconv.FormatInt(info.FreeLeft, 10)}}},
			{Type: sdk.BlockTable, Title: "奖品", Columns: []string{"名称", "内容"}, Rows: rows},
		}})
	})
	r.GET("/info", func(c *gin.Context) {
		ctx := c.Request.Context()
		s, err := load(ctx, h)
		if err != nil {
			sdk.Fail(c, http.StatusServiceUnavailable, "活动暂未开放")
			return
		}
		info, err := userInfo(ctx, h, s, sdk.CurrentUser(c).ID)
		if err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, info)
	})
	r.POST("/draw", func(c *gin.Context) {
		var in struct {
			SubscriptionID int64  `json:"subscription_id"`
			Pay            string `json:"pay"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || in.SubscriptionID <= 0 {
			sdk.Fail(c, http.StatusUnprocessableEntity, "请选择订阅")
			return
		}
		ctx := c.Request.Context()
		s, err := load(ctx, h)
		if err != nil {
			sdk.Fail(c, http.StatusServiceUnavailable, "活动暂未开放")
			return
		}
		uid := sdk.CurrentUser(c).ID
		// 同一用户同时只放行一个请求进入数据库事务，其余立即返回，避免排队等锁占满数据库连接
		lockKey, cdKey := "lock:"+strconv.FormatInt(uid, 10), "cd:"+strconv.FormatInt(uid, 10)
		ok, err := h.KV().SetNX(ctx, lockKey, "1", 10*time.Second)
		if err != nil {
			// 并发写同一个键时 MySQL 可能报死锁，同样视为请求过于频繁
			h.Log().Warn("抽奖标记写入失败", zap.Int64("user", uid), zap.Error(err))
		}
		if !ok {
			sdk.Fail(c, http.StatusTooManyRequests, "操作太频繁，请稍后再试")
			return
		}
		defer h.KV().Delete(context.WithoutCancel(ctx), lockKey)
		if _, err := h.KV().Get(ctx, cdKey); err == nil {
			sdk.Fail(c, http.StatusTooManyRequests, "抽奖太频繁，请稍后再试")
			return
		}
		res, err := draw(ctx, h, s, uid, in.SubscriptionID, in.Pay)
		if err == nil && Cooldown > 0 {
			_ = h.KV().Set(context.WithoutCancel(ctx), cdKey, "1", Cooldown)
		}
		var de drawError
		if errors.As(err, &de) {
			sdk.Fail(c, http.StatusBadRequest, string(de))
			return
		}
		if err != nil {
			h.Log().Error("抽奖失败", zap.Int64("user", uid), zap.Error(err))
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, res)
	})
	r.GET("/records", func(c *gin.Context) {
		current, size := page(c)
		q := h.DB().WithContext(c.Request.Context()).Model(&model.LuckyWheelLog{}).Where("user_id = ?", sdk.CurrentUser(c).ID)
		var total int64
		rows := []model.LuckyWheelLog{}
		if err := q.Count(&total).Error; err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		if err := q.Order("id DESC").Offset((current - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		sdk.OK(c, gin.H{"data": rows, "total": total})
	})
	// 本月消耗流量抽奖的记录（流量明细页“其它扣除”），最多 500 条
	r.GET("/traffic-costs", func(c *gin.Context) {
		y, mo, _ := time.Now().Date()
		q := h.DB().WithContext(c.Request.Context()).Model(&model.LuckyWheelLog{}).
			Select("id", "subscription_id", "cost", "created_at").
			Where("user_id = ? AND cost_type = ? AND created_at >= ?", sdk.CurrentUser(c).ID, CostTraffic, time.Date(y, mo, 1, 0, 0, 0, 0, time.Local).Unix())
		if id, _ := strconv.ParseInt(c.Query("subscription_id"), 10, 64); id > 0 {
			q = q.Where("subscription_id = ?", id)
		}
		rows := []model.LuckyWheelLog{}
		if err := q.Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, l := range rows {
			out = append(out, gin.H{"id": l.ID, "subscription_id": l.SubscriptionID, "bytes": l.Cost, "created_at": l.CreatedAt})
		}
		sdk.OK(c, out)
	})

	a := h.AdminRoutes()
	a.GET("/settings", func(c *gin.Context) {
		s, err := load(c.Request.Context(), h)
		if err != nil {
			// 配置损坏时返回默认配置供管理员重新保存
			s = defaults()
		}
		sdk.OK(c, s)
	})
	a.POST("/settings", func(c *gin.Context) {
		var s Settings
		if err := c.ShouldBindJSON(&s); err != nil {
			sdk.Fail(c, http.StatusUnprocessableEntity, "参数格式有误")
			return
		}
		if err := s.validate(); err != nil {
			sdk.Fail(c, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err := save(c.Request.Context(), h, s); err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "保存失败")
			return
		}
		sdk.OK(c, true)
	})
	a.GET("/records", func(c *gin.Context) { adminRecords(c, h) })
	return nil
}

// page 分页参数：current ≥ 1，page_size 1～100（默认 15）。
func page(c *gin.Context) (int, int) {
	current, _ := strconv.Atoi(c.Query("current"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	if current < 1 {
		current = 1
	}
	if size < 1 || size > 100 {
		size = 15
	}
	return current, size
}

type adminRecord struct {
	model.LuckyWheelLog
	Email            string `json:"email"`
	SubscriptionName string `json:"subscription_name"`
}

func adminRecords(c *gin.Context, h sdk.Host) {
	current, size := page(c)
	q := h.DB().WithContext(c.Request.Context()).Model(&model.LuckyWheelLog{})
	if email := c.Query("email"); email != "" {
		q = q.Where("user_id IN (?)", h.DB().Model(&model.User{}).Select("id").Where("email = ?", email))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
		return
	}
	var logs []model.LuckyWheelLog
	if err := q.Order("id DESC").Offset((current - 1) * size).Limit(size).Find(&logs).Error; err != nil {
		sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
		return
	}
	ids := make([]int64, 0, len(logs))
	subIDs := make([]int64, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.UserID)
		subIDs = append(subIDs, l.SubscriptionID)
	}
	var users []model.User
	var subs []model.Subscription
	if len(ids) > 0 {
		db := h.DB().WithContext(c.Request.Context())
		if err := db.Select("id", "email").Where("id IN ?", ids).Find(&users).Error; err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
		if err := db.Select("id", "name").Where("id IN ?", subIDs).Find(&subs).Error; err != nil {
			sdk.Fail(c, http.StatusInternalServerError, "服务繁忙")
			return
		}
	}
	emails := make(map[int64]string, len(users))
	for _, u := range users {
		emails[u.ID] = u.Email
	}
	names := make(map[int64]string, len(subs))
	for _, s := range subs {
		names[s.ID] = s.Name
	}
	rows := make([]adminRecord, 0, len(logs))
	for _, l := range logs {
		rows = append(rows, adminRecord{LuckyWheelLog: l, Email: emails[l.UserID], SubscriptionName: names[l.SubscriptionID]})
	}
	sdk.OK(c, gin.H{"data": rows, "total": total})
}

// clean 删除超过保留天数的抽奖记录。
func clean(ctx context.Context, h sdk.Host) error {
	s, err := load(ctx, h)
	if err != nil {
		s = defaults()
	}
	before := time.Now().AddDate(0, 0, -int(s.RetentionDays)).Unix()
	return h.DB().WithContext(ctx).Where("created_at < ?", before).Delete(&model.LuckyWheelLog{}).Error
}
