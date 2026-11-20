package api

import (
	"strconv"

	"github.com/samur005/3s-ui/service"
	"github.com/samur005/3s-ui/util/common"

	"github.com/gin-gonic/gin"
)

// GetXrayStatus returns the supervised Xray-core process state.
func (a *ApiService) GetXrayStatus(c *gin.Context) {
	var svc service.XrayService
	jsonObj(c, svc.Status(), nil)
}

// RestartXray rebuilds the config and restarts the process.
func (a *ApiService) RestartXray(c *gin.Context) {
	var svc service.XrayService
	jsonMsgObj(c, "", svc.Restart(), nil)
}

// GetXrayClient returns vless:// links for one client on every xvless inbound.
func (a *ApiService) GetXrayClient(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Query("id"), 10, 32)
	if id == 0 {
		jsonMsg(c, "", common.NewError("client id is required"))
		return
	}
	var svc service.XrayService
	links, err := svc.ClientLinks(uint(id))
	jsonObj(c, links, err)
}

// GetXrayVlessEnc returns a fresh VLESS Encryption pair (ML-KEM-768).
func (a *ApiService) GetXrayVlessEnc(c *gin.Context) {
	var svc service.XrayService
	pair, err := svc.GenerateVlessEnc()
	jsonObj(c, pair, err)
}
