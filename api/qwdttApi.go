package api

import (
	"strconv"
	"strings"

	"github.com/samur005/3s-ui/qwdtt"
	"github.com/samur005/3s-ui/service"

	"github.com/gin-gonic/gin"
)

// The qWDTT page's endpoints. All of them sit behind the panel login.

// GetQwdtt returns the configuration, live status and share link.
func (a *ApiService) GetQwdtt(c *gin.Context) {
	var svc service.QwdttService
	v, err := svc.View()
	jsonObj(c, v, err)
}

// GetQwdttLogs returns the server's recent output.
func (a *ApiService) GetQwdttLogs(c *gin.Context) {
	n, _ := strconv.Atoi(c.Query("c"))
	if n <= 0 || n > 500 {
		n = 100
	}
	jsonObj(c, qwdtt.Get().Logs(n), nil)
}

// GetQwdttSubscription returns the JSON document the qWDTT Android client
// imports.
func (a *ApiService) GetQwdttSubscription(c *gin.Context) {
	var svc service.QwdttService
	doc, err := svc.SubscriptionJSON()
	jsonObj(c, doc, err)
}

// SaveQwdtt stores and applies the configuration (form field "config").
func (a *ApiService) SaveQwdtt(c *gin.Context) {
	var svc service.QwdttService
	err := svc.Save(c.Request.FormValue("config"))
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	var v *service.QwdttView
	v, err = svc.View()
	jsonMsgObj(c, "save", v, err)
}

// RestartQwdtt restarts the server process.
func (a *ApiService) RestartQwdtt(c *gin.Context) {
	qwdtt.Get().Restart()
	var svc service.QwdttService
	v, err := svc.View()
	jsonMsgObj(c, "", v, err)
}

// SaveQwdttCookies stores the VK cookies (form field "cookies").
func (a *ApiService) SaveQwdttCookies(c *gin.Context) {
	var svc service.QwdttService
	if err := svc.SaveCookies(c.Request.FormValue("cookies")); err != nil {
		jsonMsg(c, "", err)
		return
	}
	v, err := svc.View()
	jsonMsgObj(c, "save", v, err)
}

// ClearQwdttCookies deletes the stored VK cookies.
func (a *ApiService) ClearQwdttCookies(c *gin.Context) {
	if err := qwdtt.ClearCookies(); err != nil {
		jsonMsg(c, "", err)
		return
	}
	var svc service.QwdttService
	v, err := svc.View()
	jsonMsgObj(c, "", v, err)
}

// CheckQwdttCookies asks VK whether the stored cookies still log in.
func (a *ApiService) CheckQwdttCookies(c *gin.Context) {
	if err := qwdtt.CheckCookies(); err != nil {
		pureJsonMsg(c, false, err.Error())
		return
	}
	pureJsonMsg(c, true, "")
}

// GenerateQwdttHash creates a VK call and adds its hash. With replace=true a
// full list drops its oldest entry.
func (a *ApiService) GenerateQwdttHash(c *gin.Context) {
	var svc service.QwdttService
	replace := strings.EqualFold(c.Request.FormValue("replace"), "true")
	if _, err := svc.Generate(replace); err != nil {
		jsonMsg(c, "", err)
		return
	}
	v, err := svc.View()
	jsonMsgObj(c, "save", v, err)
}

// CheckQwdttHashes asks VK whether each configured hash's call still runs.
func (a *ApiService) CheckQwdttHashes(c *gin.Context) {
	cfg, err := qwdtt.Load()
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	jsonObj(c, qwdtt.CheckHashes(cfg), nil)
}

// GetQwdttUsers lists the passwords the server holds -- the panel's
// per-client ones and any made outside it -- with devices and counters; the
// passwords themselves are not sent.
func (a *ApiService) GetQwdttUsers(c *gin.Context) {
	var svc service.QwdttService
	users, err := svc.ServerUsers()
	jsonObj(c, users, err)
}

// GetQwdttClient returns one client's qwdtt:// links (this panel's server and
// every node's), for the QR dialog. ?id=<client id>.
func (a *ApiService) GetQwdttClient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Query("id"), 10, 64)
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	var svc service.QwdttService
	links, err := svc.ClientLinksByID(uint(id))
	jsonObj(c, links, err)
}

// SaveQwdttRemote stores the remote WDTT panel hashes can be created through
// (form fields url, username, password, token; an empty url clears it, an
// empty password or token keeps the stored one).
func (a *ApiService) SaveQwdttRemote(c *gin.Context) {
	var svc service.QwdttService
	err := svc.SaveRemote(qwdtt.RemoteSource{
		URL:      c.Request.FormValue("url"),
		Username: c.Request.FormValue("username"),
		Password: c.Request.FormValue("password"),
		Token:    c.Request.FormValue("token"),
	})
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	v, err := svc.View()
	jsonMsgObj(c, "save", v, err)
}

func (a *ApiService) GetQwdttCookies(c *gin.Context) {
	var svc service.QwdttService
	cookies, err := svc.GetCookies()
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	jsonObj(c, map[string]string{"cookies": cookies}, nil)
}
