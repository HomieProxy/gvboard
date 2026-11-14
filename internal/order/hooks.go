package order

import (
	"context"

	"go.uber.org/zap"

	"gvboard/internal/hook"
	"gvboard/internal/model"
)

func orderEvent(o *model.Order) hook.Order {
	return hook.Order{
		TradeNo: o.TradeNo, UserID: o.UserID, PlanID: o.PlanID, Period: o.Period, Type: o.Type, Status: o.Status,
		TotalAmount: o.TotalAmount, BalanceAmount: val(o.BalanceAmount), SubscriptionID: o.SubscriptionID,
	}
}

// emitOrder 发出订单事件（事务已提交）。
func (s *Service) emitOrder(ctx context.Context, event string, o *model.Order) {
	if s.Hooks == nil || o == nil {
		return
	}
	s.Hooks.Emit(ctx, event, orderEvent(o))
	if event == hook.EventOrderOpened && o.SubscriptionID != nil {
		s.Hooks.Emit(ctx, hook.EventSubscriptionChanged, hook.Subscription{
			SubscriptionID: *o.SubscriptionID, UserID: o.UserID, Action: hook.SubscriptionOrder, TradeNo: o.TradeNo,
		})
	}
}

// emitOrderByNo 读取订单后发出事件。
func (s *Service) emitOrderByNo(ctx context.Context, event, tradeNo string) {
	if s.Hooks == nil {
		return
	}
	var o model.Order
	if err := s.DB.WithContext(ctx).Where("trade_no = ?", tradeNo).Take(&o).Error; err != nil {
		s.log().Warn("读取订单失败，未发出插件事件", zap.String("trade_no", tradeNo), zap.String("event", event), zap.Error(err))
		return
	}
	s.emitOrder(ctx, event, &o)
}
