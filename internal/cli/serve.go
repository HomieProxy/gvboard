package cli

import (
	"context"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"gvboard/internal/adminstat"
	"gvboard/internal/adminuser"
	"gvboard/internal/app"
	"gvboard/internal/audit"
	"gvboard/internal/captcha"
	"gvboard/internal/content"
	"gvboard/internal/invite"
	"gvboard/internal/legacy"
	"gvboard/internal/machine"
	"gvboard/internal/node"
	"gvboard/internal/nodeapi"
	"gvboard/internal/order"
	"gvboard/internal/passport"
	"gvboard/internal/payment"
	"gvboard/internal/server"
	"gvboard/internal/subscribe"
	"gvboard/internal/sysconfig"
	"gvboard/internal/telegram"
	"gvboard/internal/theme"
	"gvboard/internal/ticket"
	"gvboard/internal/usercenter"
	"gvboard/internal/web"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "启动 HTTP 服务（按配置同时运行队列处理与定时任务）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.New(cfgPath)
			if err != nil {
				return err
			}
			defer a.Close()
			a.Auth.AdminHooks = []gin.HandlerFunc{a.SysLog.Audit()}

			sv, err := newServices(a)
			if err != nil {
				return err
			}
			defer sv.close()
			for _, j := range sv.jobs() {
				if err := a.Scheduler.Add(j); err != nil {
					return err
				}
			}
			subs, orders, pays, invites, nodeAPI := sv.subs, sv.orders, sv.pays, sv.invites, sv.nodeAPI
			subscribeSvc, tickets, bot := sv.subscribe, sv.tickets, sv.bot
			r, err := server.NewRouter(a.Config, a.Log, a.I18n.Middleware())
			if err != nil {
				return err
			}
			r.RealIP.UseDynamic(func() []string { return a.Settings.Strings("trusted_proxy_cidrs") })
			contents := &content.Service{
				DB: a.DB, Settings: a.Settings, Subscriptions: subs,
				Translate: func(locale, key string) string { return a.I18n.T(locale, key, nil) },
				Hooks:     sv.plugins,
			}
			nodeAllow, err := nodeapi.ParseWhitelist(a.Config.Node.IPWhitelist)
			if err != nil {
				return err
			}
			passports := &passport.Service{
				DB:       a.DB,
				Cache:    a.Cache,
				Settings: a.Settings,
				Auth:     a.Auth,
				Mail:     a.Mail,
				Captcha:  captcha.NewReCaptcha(),
				I18n:     a.I18n,
				Hooks:    sv.plugins,
			}
			users := &usercenter.Service{DB: a.DB, Settings: a.Settings, Auth: a.Auth, Subscriptions: subs}
			nodes := &node.Service{DB: a.DB, Cache: a.Cache, Settings: a.Settings, Log: a.Log.Named("node")}
			machines := &machine.Service{DB: a.DB, Settings: a.Settings, Log: a.Log.Named("machine")}
			adminUsers := &adminuser.Service{
				DB: a.DB, Settings: a.Settings, Auth: a.Auth, Subscriptions: subs, Mail: a.Mail, Guard: subscribeSvc,
			}
			themes := theme.New(a.Config.Server.ThemeDir, a.Settings)
			sysConfig := &sysconfig.Service{
				DB: a.DB, Settings: a.Settings, Mailer: a.Mailer, Log: a.Log.Named("mail"), ThemeExists: themes.Exists,
			}
			r.Mount(
				&passport.Module{Svc: passports},
				&usercenter.Module{Svc: users},
				&order.Module{Svc: orders, Auth: a.Auth},
				&payment.Module{Svc: pays, Auth: a.Auth},
				&invite.Module{Svc: invites, Auth: a.Auth},
				&node.Module{Svc: nodes, Auth: a.Auth},
				&nodeapi.Module{Svc: nodeAPI, Auth: a.Auth, Allow: nodeAllow, Audit: sv.audit, Machines: machines},
				&machine.Module{Svc: machines, Auth: a.Auth, Allow: nodeAllow},
				&audit.Module{Svc: sv.audit, Auth: a.Auth},
				&subscribe.Module{Svc: subscribeSvc, Auth: a.Auth},
				&ticket.Module{Svc: tickets, Auth: a.Auth, Locale: a.I18n.Locale},
				&content.Module{Svc: contents, Auth: a.Auth, Locale: a.I18n.Locale},
				&telegram.Module{Svc: bot, Auth: a.Auth},
				&adminuser.Module{Svc: adminUsers},
				&sysconfig.Module{
					Svc:       sysConfig,
					Auth:      a.Auth,
					Translate: func(c *gin.Context, key string, params map[string]any) string { return a.I18n.Tr(c, key, params) },
				},
				&adminstat.Module{
					DB: a.DB, Cache: a.Cache, Settings: a.Settings, Queue: a.Queue, SysLog: a.SysLog, Auth: a.Auth,
					Jobs: a.Scheduler, RealIP: r.RealIP, Version: Version, BuildTime: BuildTime,
				},
				&legacy.Module{
					DB: a.DB, Settings: a.Settings, Auth: a.Auth, I18n: a.I18n, Log: a.Log.Named("legacy"),
					Passport: passports, Users: users, Subs: subs, Orders: orders, Payments: pays, Invites: invites,
					Tickets: tickets, Content: contents, Subscribe: subscribeSvc, Traffic: nodeAPI, Bot: bot,
					AdminUsers: adminUsers, Nodes: nodes, SysConfig: sysConfig, Cache: a.Cache, Queue: a.Queue,
					Themes: themes,
				},
				&theme.Module{Svc: themes, Auth: a.Auth},
				sv.plugins,
				// 页面托管放在最后：订阅路径等动态路由优先匹配。
				&web.Module{Settings: a.Settings, Themes: themes},
			)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			go a.Settings.Watch(ctx)
			go sv.plugins.Watch(ctx)

			var wg sync.WaitGroup
			// 订阅日志在 HTTP 服务完全停止后再结束，保证处理中的请求日志也能写入。
			logCtx, stopLog := context.WithCancel(context.Background())
			defer stopLog()
			wg.Add(2)
			go func() {
				defer wg.Done()
				subscribeSvc.RunLogWriter(logCtx)
			}()
			go func() {
				defer wg.Done()
				a.SysLog.Run(logCtx)
			}()
			if a.Config.Worker.Enabled {
				wg.Add(2)
				go func() {
					defer wg.Done()
					if err := a.Queue.Run(ctx, a.Config.Worker.Concurrency); err != nil {
						a.Log.Error("队列处理器异常退出", zap.Error(err))
						stop()
					}
				}()
				// 长轮询模式下接收 Telegram 更新，多实例时只有一个实例在轮询。
				go func() {
					defer wg.Done()
					bot.RunPoller(ctx)
				}()
			}
			if a.Config.Scheduler.Enabled {
				wg.Add(1)
				go func() {
					defer wg.Done()
					a.Scheduler.Run(ctx)
				}()
			}

			a.Log.Info("Gvboard 启动",
				zap.String("version", Version),
				zap.String("mode", a.Config.Server.Mode),
				zap.Bool("worker", a.Config.Worker.Enabled),
				zap.Bool("scheduler", a.Config.Scheduler.Enabled),
				zap.Bool("compat.v2board_api", a.Config.Compat.V2boardAPI),
				zap.Bool("compat.legacy_subscribe", a.Config.Compat.LegacySubscribe),
				zap.Bool("compat.legacy_callback", a.Config.Compat.LegacyCallback),
			)
			err = server.New(a.Config, r, a.Log).Run(ctx)
			stop()
			stopLog()
			wg.Wait()
			return err
		},
	}
}
