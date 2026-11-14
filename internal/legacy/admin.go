package legacy

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// adminGroup 原项目管理端路由 /api/v1/{secure_path}/...（决策 D58）。
// 安全路径不一致、未设置安全路径、未登录或非管理员一律返回统一 404（决策 D12，原项目为 403）；
// 凭证与原项目一样取自 auth_data 参数或 Authorization 请求头。认证通过后执行审计等管理端钩子。
func (m *Module) adminGroup(r *server.Router) *gin.RouterGroup {
	handlers := []gin.HandlerFunc{m.requireAdmin}
	return r.Legacy.Group("/:"+auth.AdminParam, append(handlers, m.Auth.AdminHooks...)...)
}

// registerAdmin 挂载原项目管理端路由（路径与请求方法与原项目 AdminRoute 一致）。
func (m *Module) registerAdmin(r *server.Router) {
	a := m.adminGroup(r)

	a.GET("/plan/fetch", m.adminPlanFetch)
	a.POST("/plan/save", m.adminPlanSave)
	a.POST("/plan/drop", m.adminPlanDrop)
	a.POST("/plan/update", m.adminPlanUpdate)
	a.POST("/plan/sort", m.adminPlanSort)

	a.GET("/config/fetch", m.adminConfigFetch)
	a.POST("/config/save", m.adminConfigSave)
	a.GET("/config/getEmailTemplate", m.adminEmailTemplates)
	a.GET("/config/getThemeTemplate", m.adminThemeTemplate)
	a.POST("/config/testSendMail", m.adminTestMail)
	a.POST("/config/setTelegramWebhook", m.adminSetTelegramWebhook)

	a.GET("/theme/getThemes", m.adminGetThemes)
	a.POST("/theme/saveThemeConfig", m.adminSaveThemeConfig)
	a.POST("/theme/getThemeConfig", m.adminGetThemeConfig)

	a.GET("/stat/getOverride", m.adminStatOverride)
	a.GET("/stat/getOrder", m.adminStatOrder)
	a.GET("/stat/getServerLastRank", m.adminServerRank("yesterday"))
	a.GET("/stat/getServerTodayRank", m.adminServerRank("today"))
	a.GET("/stat/getUserLastRank", m.adminUserRank("yesterday"))
	a.GET("/stat/getUserTodayRank", m.adminUserRank("today"))
	a.GET("/stat/getStatUser", m.adminStatUser)
	a.GET("/stat/getStat", m.missingMethod)
	a.GET("/stat/getRanking", m.missingMethod)
	a.GET("/stat/getStatRecord", m.missingMethod)

	a.GET("/system/getSystemStatus", m.adminSystemStatus)
	a.GET("/system/getQueueStats", m.adminQueueStats)
	a.GET("/system/getQueueWorkload", m.adminQueueWorkload)
	a.GET("/system/getQueueMasters", m.adminQueueMasters)
	a.GET("/system/getSystemLog", m.adminSystemLog)

	a.GET("/server/group/fetch", m.adminGroupFetch)
	a.POST("/server/group/save", m.adminGroupSave)
	a.POST("/server/group/drop", m.adminGroupDrop)
	a.GET("/server/route/fetch", m.adminRouteFetch)
	a.POST("/server/route/save", m.adminRouteSave)
	a.POST("/server/route/drop", m.adminRouteDrop)
	a.GET("/server/manage/getNodes", m.adminGetNodes)
	a.POST("/server/manage/sort", m.adminServerSort)
	for _, typ := range serverTypes {
		a.POST("/server/"+typ+"/save", m.adminServerSave(typ))
		a.POST("/server/"+typ+"/drop", m.adminServerDrop(typ))
		a.POST("/server/"+typ+"/update", m.adminServerUpdate(typ))
		a.POST("/server/"+typ+"/copy", m.adminServerCopy(typ))
	}

	a.GET("/order/fetch", m.adminOrderFetch)
	a.POST("/order/update", m.adminOrderUpdate)
	a.POST("/order/assign", m.adminOrderAssign)
	a.POST("/order/paid", m.adminOrderPaid)
	a.POST("/order/cancel", m.adminOrderCancel)
	a.POST("/order/detail", m.adminOrderDetail)

	a.GET("/user/fetch", m.adminUserFetch)
	a.POST("/user/update", m.adminUserUpdate)
	a.GET("/user/getUserInfoById", m.adminUserInfo)
	a.POST("/user/generate", m.adminUserGenerate)
	a.POST("/user/dumpCSV", m.adminUserDumpCSV)
	a.POST("/user/sendMail", m.adminUserSendMail)
	a.POST("/user/ban", m.adminUserBan)
	a.POST("/user/resetSecret", m.adminUserResetSecret)
	a.POST("/user/delUser", m.adminUserDel)
	a.POST("/user/allDel", m.adminUserAllDel)
	a.POST("/user/setInviteUser", m.missingMethod)

	a.GET("/notice/fetch", m.adminNoticeFetch)
	a.POST("/notice/save", m.adminNoticeSave)
	a.POST("/notice/update", m.missingMethod)
	a.POST("/notice/drop", m.adminNoticeDrop)
	a.POST("/notice/show", m.adminNoticeShow)

	a.GET("/ticket/fetch", m.adminTicketFetch)
	a.POST("/ticket/reply", m.adminTicketReply)
	a.POST("/ticket/close", m.adminTicketClose)

	a.GET("/coupon/fetch", m.adminCouponFetch)
	a.POST("/coupon/generate", m.adminCouponGenerate)
	a.POST("/coupon/drop", m.adminCouponDrop)
	a.POST("/coupon/show", m.adminCouponShow)

	a.GET("/giftcard/fetch", m.adminGiftcardFetch)
	a.POST("/giftcard/generate", m.adminGiftcardGenerate)
	a.POST("/giftcard/drop", m.adminGiftcardDrop)

	a.GET("/payment/fetch", m.adminPaymentFetch)
	a.GET("/payment/getPaymentMethods", m.adminPaymentMethods)
	a.POST("/payment/getPaymentForm", m.adminPaymentForm)
	a.POST("/payment/save", m.adminPaymentSave)
	a.POST("/payment/drop", m.adminPaymentDrop)
	a.POST("/payment/show", m.adminPaymentShow)
	a.POST("/payment/sort", m.adminPaymentSort)

	a.GET("/knowledge/fetch", m.adminKnowledgeFetch)
	a.GET("/knowledge/getCategory", m.adminKnowledgeCategory)
	a.POST("/knowledge/save", m.adminKnowledgeSave)
	a.POST("/knowledge/show", m.adminKnowledgeShow)
	a.POST("/knowledge/drop", m.adminKnowledgeDrop)
	a.POST("/knowledge/sort", m.adminKnowledgeSort)
}

func (m *Module) requireAdmin(c *gin.Context) {
	want := m.Settings.String("secure_path")
	if want == "" || subtle.ConstantTimeCompare([]byte(c.Param(auth.AdminParam)), []byte(want)) != 1 {
		httpx.NotFound(c)
		return
	}
	raw := params(c).str("auth_data")
	if raw == "" {
		raw = c.GetHeader("Authorization")
	}
	id, err := m.Auth.Parse(c.Request.Context(), raw)
	if err != nil || !id.IsAdmin {
		httpx.NotFound(c)
		return
	}
	auth.SetCurrent(c, id)
	c.Next()
}

// abortRaw 原项目管理端 abort(500, '中文消息')，消息不翻译。
func abortRaw(msg string) error {
	return &abortError{status: http.StatusInternalServerError, key: msg, raw: true}
}

// paged 原项目分页列表 {"data": [...], "total": n}。
func paged(c *gin.Context, data any, total int64) {
	c.JSON(http.StatusOK, gin.H{"data": data, "total": total})
}

// page 原项目分页参数：current 默认 1，pageSize 小于 10 时为 10。
func (in input) page() (current, size int) {
	current = int(in.intOr("current", 1))
	if current < 1 {
		current = 1
	}
	size = int(in.intOr("pageSize", 0))
	if size < 10 {
		size = 10
	}
	return current, size
}
