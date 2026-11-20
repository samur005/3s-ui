package api

import (
	"strconv"

	"github.com/samur005/3s-ui/service"
	"github.com/samur005/3s-ui/util/common"
	"github.com/samur005/3s-ui/wgserver"

	"github.com/gin-gonic/gin"
)

// The WireGuard / AmneziaWG endpoints. All of them sit behind the panel login.

func queryUint(c *gin.Context, key string) uint {
	n, _ := strconv.ParseUint(c.Query(key), 10, 32)
	return uint(n)
}

// GetWgStatus returns the live state of one inbound (query "id").
func (a *ApiService) GetWgStatus(c *gin.Context) {
	var svc service.WgService
	st, err := svc.Status(queryUint(c, "id"))
	jsonObj(c, st, err)
}

// GetWgClient returns a client's configs, links and .conf text on every
// WireGuard / AmneziaWG inbound it is bound to (query "id" = client id).
func (a *ApiService) GetWgClient(c *gin.Context) {
	var svc service.WgService
	id := queryUint(c, "id")
	if id == 0 {
		jsonMsg(c, "", common.NewError("client id is required"))
		return
	}
	entries, err := svc.ClientConfigs(id)
	jsonObj(c, entries, err)
}

// GetWgGenerate returns a fresh random AmneziaWG parameter set (query
// "version").
func (a *ApiService) GetWgGenerate(c *gin.Context) {
	var svc service.WgService
	v, err := svc.GenerateObfuscation(c.Query("version"))
	jsonObj(c, v, err)
}

// RestartWg rebuilds one inbound's device (form field "id").
func (a *ApiService) RestartWg(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Request.FormValue("id"), 10, 32)
	wgserver.Get().Restart(uint(id))
	var svc service.WgService
	st, err := svc.Status(uint(id))
	jsonMsgObj(c, "", st, err)
}
