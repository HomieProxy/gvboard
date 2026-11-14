package cli

import (
	"context"

	"github.com/gin-gonic/gin"

	"gvboard/internal/app"
	"gvboard/internal/geoip"
	"gvboard/internal/httpx"
	"gvboard/internal/invite"
	"gvboard/internal/nodeapi"
	"gvboard/internal/order"
	"gvboard/internal/payment"
	"gvboard/internal/plugin"
	"gvboard/internal/scheduler"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/tasks"
	"gvboard/internal/telegram"
	"gvboard/internal/ticket"
	sdk "gvboard/pkg/plugin"
)

// services 各业务服务（serve 与命令行工具共用）。
type services struct {
	subs      *subscription.Service
	orders    *order.Service
	pays      *payment.Service
	invites   *invite.Service
	nodeAPI   *nodeapi.Service
	subscribe *subscribe.Service
	tickets   *ticket.Service
	bot       *telegram.Service
	tasks     *tasks.Service
	plugins   *plugin.Manager
	geo       geoip.Locator
}

// newServices 创建业务服务并注册队列任务。调用方负责 close。
func newServices(a *app.App) (*services, error) {
	httpx.SetTranslator(func(c *gin.Context, key string, params map[string]any) string {
		return a.I18n.Tr(c, key, params)
	})
	translate := func(locale, key string, params map[string]any) string { return a.I18n.T(locale, key, params) }
	sv := &services{}
	sv.subs = &subscription.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings,
		Linker: &subscription.Linker{Settings: a.Settings, Cache: a.Cache},
	}
	sv.orders = &order.Service{DB: a.DB, Settings: a.Settings, Subscriptions: sv.subs, Log: a.Log.Named("order")}
	order.RegisterTask(a.Queue, sv.orders)
	sv.pays = &payment.Service{
		DB: a.DB, Settings: a.Settings, Orders: sv.orders,
		APIPrefix: a.Config.API.Prefix, Log: a.Log.Named("payment"),
	}
	sv.orders.Payer = sv.pays
	sv.invites = &invite.Service{DB: a.DB, Settings: a.Settings, Log: a.Log.Named("invite")}
	sv.nodeAPI = &nodeapi.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings, Queue: a.Queue, Log: a.Log.Named("nodeapi"),
	}
	nodeapi.RegisterTask(a.Queue, sv.nodeAPI)
	sv.subscribe = &subscribe.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings, Linker: sv.subs.Linker, Log: a.Log.Named("subscribe"),
	}
	sv.tickets = &ticket.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings, Subscriptions: sv.subs, Mail: a.Mail,
		Translate: translate, Log: a.Log.Named("ticket"),
	}
	geo, err := geoip.Open(a.Config.GeoIP)
	if err != nil {
		return nil, err
	}
	sv.geo = geo
	sv.bot = &telegram.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings, Queue: a.Queue, I18n: a.I18n,
		Subscribe: sv.subscribe, Tickets: sv.tickets, Geo: geo,
		Subscriptions: sv.subs, Orders: sv.orders, Invites: sv.invites, Payments: sv.pays,
		APIPrefix: a.Config.API.Prefix, Log: a.Log.Named("telegram"),
	}
	telegram.RegisterTask(a.Queue, sv.bot)
	sv.pays.OnPaid = sv.bot.OnPaid
	sv.tickets.OnEvent = sv.bot.OnTicket
	sv.tickets.OnAdminReply = sv.bot.OnAdminReply
	sv.tasks = &tasks.Service{
		DB: a.DB, Cache: a.Cache, Settings: a.Settings, Mail: a.Mail, Notify: sv.bot, Translate: translate,
		CleanSubscribeLogs: sv.subscribe.CleanLogs, Log: a.Log.Named("tasks"),
	}
	sv.plugins = &plugin.Manager{
		DB: a.DB, Cache: a.Cache, Auth: a.Auth, Settings: a.Settings, Log: a.Log.Named("plugin"),
		Middleware: []gin.HandlerFunc{a.I18n.Middleware()},
	}
	sv.plugins.RegisterTask(a.Queue)
	if err := sv.plugins.Load(context.Background(), sdk.Registered()); err != nil {
		sv.close()
		return nil, err
	}
	sv.orders.Hooks, sv.subs.Hooks, sv.subscribe.Hooks = sv.plugins, sv.plugins, sv.plugins
	sv.nodeAPI.Hooks, sv.tickets.Hooks, a.Mailer.Hooks = sv.plugins, sv.plugins, sv.plugins
	return sv, nil
}

func (sv *services) close() {
	if sv.plugins != nil {
		sv.plugins.Close()
	}
	if sv.geo != nil {
		sv.geo.Close()
	}
}

// jobs 全部定时任务（对应原项目 app/Console/Kernel.php）。
func (sv *services) jobs() []scheduler.Job {
	return append([]scheduler.Job{
		sv.nodeAPI.FlushJob(),
		sv.orders.CheckJob(),
		sv.invites.CheckJob(),
		sv.tickets.CheckJob(),
		sv.orders.RenewalJob(),
	}, append(sv.tasks.Jobs(), sv.plugins.Jobs()...)...)
}
