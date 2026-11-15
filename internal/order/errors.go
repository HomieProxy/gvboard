package order

import "gvboard/internal/httpx"

// 用户端错误，文案为原项目语言包中的键。
var (
	errInvalidParams      = httpx.Validation("Invalid parameters")
	errUserNotExist       = httpx.NotFoundErr("The user does not exist")
	errPlanIDRequired     = httpx.Validation("Plan ID cannot be empty")
	errPeriodRequired     = httpx.Validation("Plan period cannot be empty")
	errWrongPeriod        = httpx.Validation("Wrong plan period")
	errPending            = httpx.Conflict("You have an unpaid or pending order, please try again later or cancel it")
	errDepositMin         = httpx.Validation("Failed to create order, deposit amount must be greater than 0")
	errDepositMax         = httpx.Validation("Deposit amount too large, please contact the administrator")
	errPlanNotExist       = httpx.BadRequest("Subscription plan does not exist")
	errSoldOut            = httpx.BadRequest("Current product is sold out")
	errPeriodUnavailable  = httpx.BadRequest("This payment period cannot be purchased, please choose another period")
	errResetUnavailable   = httpx.BadRequest("Subscription has expired or no active subscription, unable to purchase Data Reset Package")
	errPlanSoldOut        = httpx.BadRequest("This subscription has been sold out, please choose another subscription")
	errCannotRenew        = httpx.BadRequest("This subscription cannot be renewed, please change to another subscription")
	errPlanExpired        = httpx.BadRequest("This subscription has expired, please change to another subscription")
	errPlanChangeDisabled = httpx.Forbidden("Changing the subscription is not allowed at present, please contact customer service or submit a ticket")
	errInsufficient       = httpx.BadRequest("Insufficient balance")
	errOrderNotExist      = httpx.NotFoundErr("Order does not exist")
	errOrderNotPending    = httpx.BadRequest("Order does not exist or has been paid")
	errOnlyCancelPending  = httpx.BadRequest("You can only cancel pending orders")
	errCancelFailed       = httpx.BadRequest("Cancel failed")
	errPaymentUnavailable = httpx.BadRequest("Payment method is not available")

	errCouponRequired   = httpx.Validation("Coupon cannot be empty")
	errCouponInvalid    = httpx.BadRequest("Invalid coupon")
	errCouponUsedUp     = httpx.BadRequest("This coupon is no longer available")
	errCouponNotStarted = httpx.BadRequest("This coupon has not yet started")
	errCouponExpired    = httpx.BadRequest("This coupon has expired")
	errCouponPlan       = httpx.BadRequest("The coupon code cannot be used for this subscription")
	errCouponPeriod     = httpx.BadRequest("The coupon code cannot be used for this period")
	errCouponPerUser    = httpx.BadRequest("The coupon can only be used :limit_use_with_user per person")
	errCouponFailed     = httpx.BadRequest("Coupon failed")
)

// 管理端错误。管理端只有中文界面，与原项目一样直接使用中文文案。
var (
	errAdminInvalid      = httpx.Validation("参数有误")
	errAdminPlanNotExist = httpx.NotFoundErr("该订阅不存在")
	errAdminPlanPrice    = httpx.Validation("金额超出范围（最大 21474836.47）")
	errAdminPlanRange    = httpx.Validation("流量、设备数、限速或容纳用户量超出范围")
	errAdminPlanHasOrder = httpx.Conflict("该订阅下存在订单无法删除")
	errAdminPlanHasSub   = httpx.Conflict("该订阅下存在用户无法删除")
	errAdminCouponNone   = httpx.NotFoundErr("优惠券不存在")
	errAdminCouponDup    = httpx.Conflict("券码已存在")
	errAdminOrderNone    = httpx.NotFoundErr("订单不存在")
	errAdminOnlyPending  = httpx.BadRequest("只能对待支付的订单进行操作")
	// errAdminOnlyCancelled 补单只针对已取消的订单。
	errAdminOnlyCancelled  = httpx.BadRequest("只能对已取消的订单补单")
	errAdminRestoreBalance = httpx.BadRequest("用户余额不足以扣回下单时抵扣的余额，无法补单")
	errAdminUserNone       = httpx.NotFoundErr("该用户不存在")
	errAdminUserPending    = httpx.Conflict("该用户还有待支付的订单，无法分配")
	errAdminResetNeedSub   = httpx.Validation("购买流量重置包必须指定订阅")
)
