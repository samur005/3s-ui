package api

import (
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/samur005/3s-ui/service"
	"github.com/samur005/3s-ui/util/common"

	"github.com/gin-gonic/gin"
)

// ---------- node side: answered to a managing master over apiv2 ----------

func (a *ApiService) NodeHello(c *gin.Context) {
	jsonObj(c, a.ServerService.NodeHelloInfo(), nil)
}

func (a *ApiService) NodeCapabilities(c *gin.Context) {
	jsonObj(c, a.ServerService.NodeCapabilitiesInfo(), nil)
}

func (a *ApiService) NodeHealth(c *gin.Context) {
	jsonObj(c, a.ServerService.NodeHealthInfo(), nil)
}

func (a *ApiService) NodeServices(c *gin.Context) {
	jsonObj(c, a.ServerService.NodeServicesInfo(), nil)
}

func (a *ApiService) NodePortCheck(c *gin.Context) {
	res, err := a.ServerService.CheckPort(c.Query("port"), c.Query("network"), c.Query("exclude"))
	jsonObj(c, res, err)
}

func (a *ApiService) NodeCheckConfig(c *gin.Context) {
	data := c.Request.FormValue("data")
	err := a.ConfigService.CheckBaseConfig(json.RawMessage(data))
	jsonMsg(c, "checkConfig", err)
}

// ---------- master side: the node management endpoints ----------

func parseNodeId(c *gin.Context, v string) (uint, bool) {
	id, err := strconv.ParseUint(v, 10, 32)
	if err != nil || id == 0 {
		jsonMsg(c, "", common.NewError("invalid node id"))
		return 0, false
	}
	return uint(id), true
}

// NodeRemote runs one typed remote operation (service/nodeRemote.go).
// Form: id, op, object, action, data (JSON), params (JSON object of strings),
// confirm (bool).
func (a *ApiService) NodeRemote(c *gin.Context, loginUser string) {
	id, ok := parseNodeId(c, c.Request.FormValue("id"))
	if !ok {
		return
	}
	req := service.RemoteRequest{
		NodeId: id,
		Op:     c.Request.FormValue("op"),
		Object: c.Request.FormValue("object"),
		Action: c.Request.FormValue("action"),
		Params: url.Values{},
	}
	if d := c.Request.FormValue("data"); d != "" {
		req.Data = json.RawMessage(d)
	}
	if p := c.Request.FormValue("params"); p != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			jsonMsg(c, "", common.NewError("params must be a JSON object of strings"))
			return
		}
		for k, v := range m {
			req.Params.Set(k, v)
		}
	}
	if v := c.Request.FormValue("confirm"); v != "" {
		req.Confirm, _ = strconv.ParseBool(v)
	}
	var svc service.NodeRemoteService
	obj, err := svc.Execute(loginUser, req)
	if err != nil {
		jsonMsg(c, "", err)
		return
	}
	jsonObj(c, obj, nil)
}

func (a *ApiService) NodeDetail(c *gin.Context) {
	id, ok := parseNodeId(c, c.Query("id"))
	if !ok {
		return
	}
	var svc service.NodeRemoteService
	out, err := svc.Detail(id)
	jsonObj(c, out, err)
}

func (a *ApiService) NodeOps(c *gin.Context) {
	var id uint
	if v := c.Query("id"); v != "" {
		var ok bool
		if id, ok = parseNodeId(c, v); !ok {
			return
		}
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	var svc service.NodeRemoteService
	rows, total, err := svc.ListOperations(id, limit, offset)
	jsonObj(c, map[string]interface{}{"rows": rows, "total": total}, err)
}

func (a *ApiService) NodeBulk(c *gin.Context, loginUser string) {
	var ids []uint
	if err := json.Unmarshal([]byte(c.Request.FormValue("ids")), &ids); err != nil {
		jsonMsg(c, "", common.NewError("ids must be a JSON array of node ids"))
		return
	}
	var svc service.NodeRemoteService
	res, err := svc.Bulk(loginUser, ids, c.Request.FormValue("op"), c.Request.FormValue("value"))
	jsonObj(c, res, err)
}

func (a *ApiService) NodeReconnect(c *gin.Context) {
	id, ok := parseNodeId(c, c.Request.FormValue("id"))
	if !ok {
		return
	}
	var svc service.NodeRemoteService
	st, err := svc.Reconnect(id)
	jsonObj(c, st, err)
}

func (a *ApiService) NodeTokenRotate(c *gin.Context, loginUser string) {
	id, ok := parseNodeId(c, c.Request.FormValue("id"))
	if !ok {
		return
	}
	var svc service.NodeRemoteService
	err := svc.RotateToken(loginUser, id)
	jsonMsg(c, "nodeTokenRotate", err)
}
