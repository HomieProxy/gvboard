package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/setting"
)

// 用户端错误，文案为原项目语言包中的键。
var (
	errPaymentUnavailable = httpx.BadRequest("Payment method is not available")
	errStripeNotFound     = httpx.NotFoundErr("payment is not found")
)

// Service 支付服务，实现 order.Payer。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Orders   *order.Service
	// APIPrefix 原生 API 前缀，用于生成新的回调地址 {api}/payment/notify/{method}/{uuid}。
	APIPrefix string
	HTTP      *http.Client
	Log       *zap.Logger
	Now       func() time.Time
	// OnPaid 回调确认收款后调用（Telegram 通知管理员等），只在订单由待支付变为已支付时触发一次。
	OnPaid func(ctx context.Context, o *model.Order)
	// OnCancelledPaid 已取消的订单收到验签通过的支付回调时调用（决策 D72）：订单不做任何修改，只通知管理员补单。
	OnCancelledPaid func(ctx context.Context, o *model.Order, paymentName, callbackNo string)
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop()
}

func (s *Service) env(ctx context.Context, p *model.Payment) *Env {
	c := s.HTTP
	if c == nil {
		c = defaultHTTP
	}
	return &Env{Ctx: ctx, Config: loadConfig(p.Config), HTTP: c, Now: s.now(), AppName: s.Settings.String("app_name")}
}

func loadConfig(j model.JSON) Config {
	c := Config{}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.UseNumber()
	_ = dec.Decode(&c)
	return c
}

// ---------- 发起支付 ----------

var (
	reOrigin = regexp.MustCompile(`(?i)^https?://[A-Za-z0-9.\-:\[\]]+$`)
	reHost   = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)
)

// currentBase 前端站点地址（原项目 PaymentService::currentBase）：优先 Origin，其次转发的主机名。
func currentBase(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && reOrigin.MatchString(o) {
		return strings.TrimRight(o, "/")
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Header.Get("X-Original-Host")
	}
	if host == "" {
		host = r.Host
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	if host == "" || !reHost.MatchString(host) {
		return ""
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	return scheme + "://" + host
}

// SiteURL 当前请求的站点地址（原项目 url()），开启 force_https 时强制 https。
func (s *Service) SiteURL(r *http.Request) string {
	if r == nil {
		return strings.TrimRight(s.Settings.String("app_url"), "/")
	}
	scheme := "http"
	if s.Settings.Bool("force_https") || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// NotifyPath 回调路径（不含域名）。
func (s *Service) NotifyPath(p *model.Payment) string {
	return s.APIPrefix + "/payment/notify/" + p.Payment + "/" + p.UUID
}

// NotifyURL 回调地址：设置了自定义通知域名时替换协议与主机部分（与原项目一致，直接拼接路径）。
func (s *Service) NotifyURL(site string, p *model.Payment) string {
	if p.NotifyDomain != nil && *p.NotifyDomain != "" {
		return *p.NotifyDomain + s.NotifyPath(p)
	}
	return site + s.NotifyPath(p)
}

// Pay 结算时发起在线支付（原项目 user/order/checkout 的支付部分）：计算手续费、记录支付方式后调用网关。
func (s *Service) Pay(ctx context.Context, o *model.Order, in order.PayInput) (int, any, error) {
	db := s.DB.WithContext(ctx)
	var p model.Payment
	if err := db.Take(&p, "id = ?", in.Method).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, errPaymentUnavailable
		}
		return 0, nil, err
	}
	g, ok := Lookup(p.Payment)
	if p.Enable != 1 || !ok {
		return 0, nil, errPaymentUnavailable
	}

	var handling *int64
	if p.HandlingFeePercent != nil || (p.HandlingFeeFixed != nil && *p.HandlingFeeFixed != 0) {
		var pct, fixed float64
		if p.HandlingFeePercent != nil {
			pct = *p.HandlingFeePercent
		}
		if p.HandlingFeeFixed != nil {
			fixed = float64(*p.HandlingFeeFixed)
		}
		v := int64(math.Round(float64(o.TotalAmount)*(pct/100) + fixed))
		handling = &v
	}
	if err := db.Model(&model.Order{}).Where("id = ?", o.ID).
		Updates(map[string]any{"handling_amount": handling, "payment_id": p.ID}).Error; err != nil {
		s.log().Error("保存订单支付方式失败", zap.String("trade_no", o.TradeNo), zap.Error(err))
		return 0, nil, gatewayError("Request failed, please try again later")
	}
	total := o.TotalAmount
	if handling != nil {
		total += *handling
	}

	var u model.User
	if err := db.Select("id", "email").Take(&u, "id = ?", o.UserID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil, err
	}
	po := &Order{
		TradeNo:     o.TradeNo,
		TotalAmount: total,
		UserID:      o.UserID,
		Email:       u.Email,
		StripeToken: in.Token,
		ClientIP:    in.ClientIP,
	}
	site := s.SiteURL(in.Request)
	po.NotifyURL = s.NotifyURL(site, &p)
	base := ""
	if in.Request != nil {
		base = currentBase(in.Request)
		po.UserAgent = in.Request.UserAgent()
	}
	if base == "" {
		base = site
	}
	po.ReturnURL = base + "/#/order/" + o.TradeNo

	res, err := g.Pay(s.env(ctx, &p), po)
	if err != nil {
		return 0, nil, err
	}
	return res.Type, res.Data, nil
}

// ---------- 用户端 ----------

// Method 用户端可用的支付方式。
type Method struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Payment            string   `json:"payment"`
	Icon               *string  `json:"icon"`
	HandlingFeeFixed   *int64   `json:"handling_fee_fixed"`
	HandlingFeePercent *float64 `json:"handling_fee_percent"`
}

// bySort 按 sort 升序，NULL 在前（与 MySQL 一致，PostgreSQL 默认 NULL 在后）。
func bySort(db *gorm.DB) *gorm.DB {
	return db.Order("CASE WHEN sort IS NULL THEN 0 ELSE 1 END").Order("sort").Order("id")
}

// Methods 已启用的支付方式（原项目 user/order/getPaymentMethod）。网关不可用（如插件已停用）的不返回。
func (s *Service) Methods(ctx context.Context) ([]Method, error) {
	var all []Method
	err := bySort(s.DB.WithContext(ctx).Model(&model.Payment{})).
		Where("enable = ?", 1).
		Select("id", "name", "payment", "icon", "handling_fee_fixed", "handling_fee_percent").
		Find(&all).Error
	out := make([]Method, 0, len(all))
	for _, m := range all {
		if _, ok := Lookup(m.Payment); ok {
			out = append(out, m)
		}
	}
	return out, err
}

// StripePublicKey StripeCredit 支付方式的公钥（原项目 user/comm/getStripePublicKey）。
func (s *Service) StripePublicKey(ctx context.Context, id int64) (any, error) {
	var p model.Payment
	err := s.DB.WithContext(ctx).Where("id = ? AND payment = ?", id, "StripeCredit").Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errStripeNotFound
	}
	if err != nil {
		return nil, err
	}
	return loadConfig(p.Config)["stripe_pk_live"], nil
}

// ---------- 支付回调 ----------

// errNotifyRejected 回调被拒绝（支付方式不存在 / 已停用 / 验签失败 / 订单不存在），统一按 404 响应。
var errNotifyRejected = errors.New("payment: 回调被拒绝")

// Notify 处理支付平台回调，返回应答内容。任何失败都返回 errNotifyRejected（原因只写日志）。
func (s *Service) Notify(ctx context.Context, method, uuid string, n *Notification) (string, error) {
	log := s.log().With(zap.String("method", method))
	var p model.Payment
	if err := s.DB.WithContext(ctx).Where("uuid = ? AND payment = ?", uuid, method).Take(&p).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Error("查询支付方式失败", zap.Error(err))
		}
		return "", errNotifyRejected
	}
	g, ok := Lookup(method)
	if !ok || p.Enable != 1 {
		log.Info("回调的支付方式未启用或网关不存在", zap.Int64("payment_id", p.ID))
		return "", errNotifyRejected
	}
	env := s.env(ctx, &p)
	if missingSecret(g, env.Config) {
		log.Warn("支付方式未配置回调验签所需的密钥，拒绝回调", zap.Int64("payment_id", p.ID))
		return "", errNotifyRejected
	}
	paid, err := g.Notify(env, n)
	if err != nil {
		log.Info("回调校验未通过", zap.Int64("payment_id", p.ID), zap.Error(err))
		return "", errNotifyRejected
	}

	var o model.Order
	if err := s.DB.WithContext(ctx).Where("trade_no = ?", paid.TradeNo).Take(&o).Error; err != nil {
		log.Warn("回调的订单不存在", zap.String("trade_no", paid.TradeNo), zap.Error(err))
		return "", errNotifyRejected
	}
	if paid.Amount != nil {
		want := o.TotalAmount
		if o.HandlingAmount != nil {
			want += *o.HandlingAmount
		}
		if *paid.Amount != want {
			log.Warn("回调金额与订单应付金额不符", zap.String("trade_no", o.TradeNo),
				zap.Int64("want", want), zap.Int64("got", *paid.Amount))
			return "", errNotifyRejected
		}
	}
	if o.Status == model.OrderStatusPending {
		changed, err := s.Orders.MarkPaid(ctx, o.TradeNo, paid.CallbackNo)
		if err != nil {
			log.Error("标记订单已支付失败", zap.String("trade_no", o.TradeNo), zap.Error(err))
			return "", errNotifyRejected
		}
		if changed && s.OnPaid != nil {
			s.OnPaid(ctx, &o)
		}
	} else if o.Status == model.OrderStatusCancelled {
		log.Warn("已取消的订单收到支付回调，未做处理，需管理员确认后补单",
			zap.String("trade_no", o.TradeNo), zap.String("callback_no", paid.CallbackNo))
		if s.OnCancelledPaid != nil {
			s.OnCancelledPaid(ctx, &o, p.Name, paid.CallbackNo)
		}
	}
	if paid.Reply != "" {
		return paid.Reply, nil
	}
	return "success", nil
}
