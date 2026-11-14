package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/queue"
	"gvboard/internal/scheduler"
	"gvboard/internal/subscription"
)

// pendingTimeout 待支付订单超时自动取消的时间（与原项目一致：2 小时）。
const pendingTimeout = 2 * 3600

// Handle 处理订单（原项目 OrderHandleJob）：待支付超时则取消，已支付则开通。
func (s *Service) Handle(ctx context.Context, tradeNo string) error {
	var o model.Order
	err := s.DB.WithContext(ctx).Where("trade_no = ?", tradeNo).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch o.Status {
	case model.OrderStatusPending:
		if o.CreatedAt <= s.now().Unix()-pendingTimeout {
			_, err = s.cancel(ctx, &o)
		}
	case model.OrderStatusProcessing:
		err = s.open(ctx, o.ID)
	}
	return err
}

// open 开通已支付订单（原项目 OrderService::open）。订单行加锁并确认仍为开通中，重复执行不会重复开通。
func (s *Service) open(ctx context.Context, orderID int64) error {
	var opened *model.Order
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&o, orderID).Error; err != nil {
			return err
		}
		if o.Status != model.OrderStatusProcessing {
			return nil
		}
		if _, err := lockUser(tx, o.UserID); err != nil {
			return err
		}
		if o.Type == model.OrderTypeDeposit {
			add := o.TotalAmount + DepositBonus(s.Settings.Strings("deposit_bounus"), o.TotalAmount)
			if err := tx.Model(&model.User{}).Where("id = ?", o.UserID).Update("balance", gorm.Expr("balance + ?", add)).Error; err != nil {
				return err
			}
			if err := tx.Model(&o).Update("status", model.OrderStatusCompleted).Error; err != nil {
				return err
			}
			o.Status = model.OrderStatusCompleted
			opened = &o
			return nil
		}

		var plan model.Plan
		if err := tx.Take(&plan, o.PlanID).Error; err != nil {
			return fmt.Errorf("订单 %s 的套餐 %d 读取失败: %w", o.TradeNo, o.PlanID, err)
		}
		sub, err := s.targetSubscription(tx, &o)
		if err != nil {
			return err
		}
		if r := val(o.RefundAmount); r != 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", o.UserID).Update("balance", gorm.Expr("balance + ?", r)).Error; err != nil {
				return err
			}
		}
		if len(o.SurplusOrderIDs) > 0 {
			if err := tx.Model(&model.Order{}).Where("id IN ?", []int64(o.SurplusOrderIDs)).
				Update("status", model.OrderStatusDiscounted).Error; err != nil {
				return err
			}
		}

		now := s.now()
		switch o.Period {
		case PeriodOnetime:
			buyByOneTime(sub, &o, &plan)
		case PeriodReset:
			resetTraffic(sub)
		default:
			buyByPeriod(sub, &o, &plan, now)
		}
		event := map[int]string{
			model.OrderTypeNew: "new_order_event_id", model.OrderTypeRenew: "renew_order_event_id", model.OrderTypeUpgrade: "change_order_event_id",
		}[o.Type]
		if event != "" && s.Settings.Int(event) == 1 {
			resetTraffic(sub)
		}
		sub.SpeedLimit = plan.SpeedLimit

		err = tx.Model(sub).Select("plan_id", "group_id", "transfer_enable", "u", "d", "device_limit", "speed_limit", "expired_at").
			Updates(sub).Error
		if err != nil {
			return err
		}
		if err := tx.Model(&o).Updates(map[string]any{"status": model.OrderStatusCompleted, "subscription_id": sub.ID}).Error; err != nil {
			return err
		}
		subID := sub.ID
		o.Status, o.SubscriptionID = model.OrderStatusCompleted, &subID
		opened = &o
		return nil
	})
	if err == nil {
		s.emitOrder(ctx, hook.EventOrderOpened, opened)
	}
	return err
}

// targetSubscription 订单作用的订阅：已指定时锁定该订阅；未指定时新开一个（不受数量上限约束，决策 D23）。
func (s *Service) targetSubscription(tx *gorm.DB, o *model.Order) (*model.Subscription, error) {
	if o.SubscriptionID == nil {
		if o.Period == PeriodReset {
			return nil, fmt.Errorf("订单 %s 为流量重置包但未指定订阅", o.TradeNo)
		}
		zero := int64(0)
		return s.Subscriptions.Create(tx, subscription.NewSubscription{UserID: o.UserID, ExpiredAt: &zero, IgnoreLimit: true})
	}
	var sub model.Subscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", *o.SubscriptionID, o.UserID).Take(&sub).Error
	if err != nil {
		return nil, fmt.Errorf("订单 %s 的订阅 %d 读取失败: %w", o.TradeNo, *o.SubscriptionID, err)
	}
	return &sub, nil
}

func resetTraffic(sub *model.Subscription) { sub.U, sub.D = 0, 0 }

// buyByPeriod 周期套餐开通（原项目 buyByPeriod）。
func buyByPeriod(sub *model.Subscription, o *model.Order, p *model.Plan, now time.Time) {
	if o.Type == model.OrderTypeUpgrade {
		ts := now.Unix()
		sub.ExpiredAt = &ts
	}
	sub.TransferEnable = p.TransferEnable * gb
	sub.DeviceLimit = p.DeviceLimit
	// 从一次性套餐转为周期套餐、新购：清零已用流量
	if sub.ExpiredAt == nil || o.Type == model.OrderTypeNew {
		resetTraffic(sub)
	}
	// 到期当天续费：清零已用流量
	if o.Type == model.OrderTypeRenew && sub.ExpiredAt != nil {
		exp := time.Unix(*sub.ExpiredAt, 0)
		if exp.Month() == now.Month() && exp.Day() == now.Day() {
			resetTraffic(sub)
		}
	}
	planID, groupID := p.ID, p.GroupID
	sub.PlanID, sub.GroupID = &planID, &groupID
	sub.ExpiredAt = expireAfter(o.Period, sub.ExpiredAt, now.Unix())
}

// buyByOneTime 一次性套餐开通（原项目 buyByOneTime）：长期有效，未折抵时保留原一次性套餐的剩余流量。
func buyByOneTime(sub *model.Subscription, o *model.Order, p *model.Plan) {
	transfer := float64(p.TransferEnable)
	if len(o.SurplusOrderIDs) == 0 {
		notUsed := float64(sub.TransferEnable-(sub.U+sub.D)) / gb
		if notUsed > 0 && sub.ExpiredAt == nil {
			transfer += notUsed
		}
	}
	resetTraffic(sub)
	sub.TransferEnable = cents(transfer * gb)
	sub.DeviceLimit = p.DeviceLimit
	planID, groupID := p.ID, p.GroupID
	sub.PlanID, sub.GroupID = &planID, &groupID
	sub.ExpiredAt = nil
}

// TaskOrderHandle 订单处理任务类型。
const TaskOrderHandle = "order_handle"

type taskPayload struct {
	TradeNo string `json:"trade_no"`
}

// RegisterTask 注册订单处理任务，并让 Paid 等操作通过队列异步开通。
// 同一订单 1 分钟内只投递一次，避免定时检查与支付回调重复堆积任务。
func RegisterTask(q *queue.Queue, s *Service) {
	q.Register(queue.Spec{Type: TaskOrderHandle, Queue: queue.QueueOrderHandle, Tries: 3, Timeout: 30 * time.Second},
		func(ctx context.Context, raw []byte) error {
			var p taskPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				s.log().Error("订单任务参数无效", zap.Error(err))
				return nil
			}
			return s.Handle(ctx, p.TradeNo)
		})
	s.Dispatch = func(ctx context.Context, tradeNo string) error {
		err := q.Enqueue(ctx, TaskOrderHandle, taskPayload{TradeNo: tradeNo}, "", asynq.Unique(time.Minute))
		if errors.Is(err, asynq.ErrDuplicateTask) {
			return nil
		}
		return err
	}
}

// CheckJob 订单检查定时任务（原项目 check:order，每分钟）：投递所有开通中的订单与超时未支付的订单。
// 原项目每分钟投递全部待支付订单，未超时的任务什么也不做，这里只投递需要处理的订单。
func (s *Service) CheckJob() scheduler.Job {
	return scheduler.Job{
		Name: "check_order", Spec: "* * * * *", NoOverlap: true,
		Run: func(ctx context.Context) error {
			var tradeNos []string
			err := s.DB.WithContext(ctx).Model(&model.Order{}).
				Where("status = ? OR (status = ? AND created_at <= ?)", model.OrderStatusProcessing,
					model.OrderStatusPending, s.now().Unix()-pendingTimeout).
				Order("created_at ASC").Pluck("trade_no", &tradeNos).Error
			if err != nil {
				return err
			}
			for _, no := range tradeNos {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if s.Dispatch != nil {
					s.dispatch(ctx, no)
					continue
				}
				if err := s.Handle(ctx, no); err != nil {
					s.log().Warn("订单处理失败", zap.String("trade_no", no), zap.Error(err))
				}
			}
			return nil
		},
	}
}
