package model

// All 返回全部数据模型，用于结构校验。
func All() []any {
	return []any{
		&User{}, &Subscription{}, &InviteCode{}, &CommissionLog{},
		&Order{}, &OrderAdjustment{}, &Plan{}, &Coupon{}, &Giftcard{}, &Payment{},
		&Notice{}, &Knowledge{}, &Ticket{}, &TicketMessage{}, &Log{}, &MailLog{},
		&Stat{}, &StatServer{}, &StatUser{},
		&ServerGroup{}, &ServerRoute{},
		&ServerShadowsocks{}, &ServerVmess{}, &ServerVless{}, &ServerTrojan{},
		&ServerTuic{}, &ServerHysteria{}, &ServerAnytls{}, &ServerV2node{},
		&Setting{}, &Plugin{}, &PluginKV{}, &PluginPackage{},
		&SubscribeRule{}, &SubscribeLog{}, &UserTelegram{},
	}
}
