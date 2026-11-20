package api

import (
	"strings"

	"github.com/samur005/3s-ui/util/common"

	"github.com/gin-gonic/gin"
)

type APIHandler struct {
	ApiService
	apiv2 *APIv2Handler
}

func NewAPIHandler(g *gin.RouterGroup, a2 *APIv2Handler) {
	a := &APIHandler{
		apiv2: a2,
	}
	a.initRouter(g)
}

func (a *APIHandler) initRouter(g *gin.RouterGroup) {
	g.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasSuffix(path, "login") && !strings.HasSuffix(path, "logout") {
			checkLogin(c)
		}
	})
	g.POST("/:postAction", a.postHandler)
	g.GET("/:getAction", a.getHandler)
}

func (a *APIHandler) postHandler(c *gin.Context) {
	loginUser := GetLoginUser(c)
	action := c.Param("postAction")

	switch action {
	case "login":
		a.ApiService.Login(c)
	case "changePass":
		a.ApiService.ChangePass(c)
	case "twoFaSetup":
		a.ApiService.TwoFaSetup(c)
	case "twoFaEnable":
		a.ApiService.TwoFaEnable(c)
	case "twoFaDisable":
		a.ApiService.TwoFaDisable(c)
	case "save":
		hostname, err := a.ApiService.canonicalHost(c)
		if err != nil {
			jsonMsg(c, "", err)
			return
		}
		a.ApiService.Save(c, loginUser, true, hostname)
	case "adoptInbounds":
		a.ApiService.AdoptInbounds(c, loginUser)
	case "reconcileNode":
		a.ApiService.ReconcileNode(c)
	case "listSyncConflicts":
		a.ApiService.ListSyncConflicts(c)
	case "resolveSyncConflict":
		a.ApiService.ResolveSyncConflict(c)
	case "enrollSync":
		a.ApiService.EnrollSync(c)
	case "unenrollSync":
		a.ApiService.UnenrollSync(c)
	case "setAutoEnroll":
		a.ApiService.SetAutoEnroll(c)
	case "getAutoEnroll":
		a.ApiService.GetAutoEnroll(c)
	case "scanRealityTarget":
		a.ApiService.ScanRealityTarget(c)
	case "listTombstones":
		a.ApiService.ListTombstones(c)
	case "purgeTombstone":
		a.ApiService.PurgeTombstone(c)
	case "restartApp":
		a.ApiService.RestartApp(c)
	case "restartSb":
		a.ApiService.RestartSb(c)
	case "maintenance":
		a.ApiService.SetMaintenance(c)
	case "updatePanel":
		a.ApiService.UpdatePanel(c)
	case "resetTraffic":
		a.ApiService.ResetTraffic(c)
	case "linkConvert":
		a.ApiService.LinkConvert(c)
	case "subConvert":
		a.ApiService.SubConvert(c)
	case "testAcme":
		a.ApiService.TestAcme(c)
	case "testNotify":
		a.ApiService.TestNotify(c)
	case "issueCert":
		a.ApiService.IssueCert(c)
	case "syncNginxProxy":
		a.ApiService.SyncNginxProxy(c)
	case "checkNginxProxy":
		a.ApiService.CheckNginxProxy(c)
	case "deleteCert":
		a.ApiService.DeleteCert(c)
	case "saveManualCert":
		a.ApiService.SaveManualCert(c)
	case "importdb":
		a.ApiService.ImportDb(c)
	case "addToken":
		a.ApiService.AddToken(c)
		a.apiv2.ReloadTokens()
	case "deleteToken":
		a.ApiService.DeleteToken(c)
		a.apiv2.ReloadTokens()
	case "getCertPing":
		a.ApiService.GetCertPing(c)
	case "closeSessions":
		a.ApiService.CloseSessions(c, true)
	case "testNode":
		a.ApiService.TestNode(c)
	case "wgRestart":
		a.ApiService.RestartWg(c)
	case "xrayRestart":
		a.ApiService.RestartXray(c)
	case "qwdttSave":
		a.ApiService.SaveQwdtt(c)
	case "qwdttRestart":
		a.ApiService.RestartQwdtt(c)
	case "qwdttCookies":
		a.ApiService.SaveQwdttCookies(c)
	case "qwdttCookiesClear":
		a.ApiService.ClearQwdttCookies(c)
	case "qwdttCookiesCheck":
		a.ApiService.CheckQwdttCookies(c)
	case "qwdttGenerate":
		a.ApiService.GenerateQwdttHash(c)
	case "qwdttHashesCheck":
		a.ApiService.CheckQwdttHashes(c)
	default:
		jsonMsg(c, "failed", common.NewError("unknown action: ", action))
	}
}

func (a *APIHandler) getHandler(c *gin.Context) {
	action := c.Param("getAction")

	switch action {
	case "logout":
		a.ApiService.Logout(c)
	case "load":
		a.ApiService.LoadData(c)
	case "inbounds", "outbounds", "endpoints", "services", "tls", "clients", "config", "nodes":
		err := a.ApiService.LoadPartialData(c, []string{action})
		if err != nil {
			jsonMsg(c, action, err)
		}
		return
	case "users":
		a.ApiService.GetUsers(c)
	case "settings":
		a.ApiService.GetSettings(c)
	case "stats":
		a.ApiService.GetStats(c)
	case "targetStats":
		a.ApiService.GetTargetStats(c)
	case "status":
		a.ApiService.GetStatus(c)
	case "onlines":
		a.ApiService.GetClusterOnlines(c)
	case "onlineIps":
		a.ApiService.GetOnlineIps(c)
	case "sessions":
		a.ApiService.GetSessions(c, true)
	case "logs":
		a.ApiService.GetLogs(c)
	case "changes":
		a.ApiService.CheckChanges(c)
	case "keypairs":
		a.ApiService.GetKeypairs(c)
	case "getdb":
		a.ApiService.GetDb(c)
	case "tokens":
		a.ApiService.GetTokens(c)
	case "singbox-config":
		a.ApiService.GetSingboxConfig(c)
	case "checkOutbound":
		a.ApiService.GetCheckOutbound(c)
	case "nodeInbounds":
		a.ApiService.GetNodeInbounds(c)
	case "detectNginx":
		a.ApiService.DetectNginx(c)
	case "certs":
		a.ApiService.GetCerts(c)
	case "updateInfo":
		a.ApiService.UpdateInfo(c)
	case "updateStatus":
		a.ApiService.UpdateStatus(c)
	case "wgStatus":
		a.ApiService.GetWgStatus(c)
	case "wgClient":
		a.ApiService.GetWgClient(c)
	case "wgGenerate":
		a.ApiService.GetWgGenerate(c)
	case "xrayStatus":
		a.ApiService.GetXrayStatus(c)
	case "xrayClient":
		a.ApiService.GetXrayClient(c)
	case "xrayVlessEnc":
		a.ApiService.GetXrayVlessEnc(c)
	case "qwdtt":
		a.ApiService.GetQwdtt(c)
	case "qwdttLogs":
		a.ApiService.GetQwdttLogs(c)
	case "qwdttSubscription":
		a.ApiService.GetQwdttSubscription(c)
	default:
		jsonMsg(c, "failed", common.NewError("unknown action: ", action))
	}
}
