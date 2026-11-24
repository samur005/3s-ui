package service

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/common"
)

// Inbounds that live on a node, managed from the master's own inbound form.
//
// The form's "Target" picks this panel or a node. For a node the inbound is
// written there through the typed remote API (save.inbounds: the node's own
// validators, X-Request-Id deduplication, audit), then adopted at once as a
// replica so it shows on the Inbounds page and takes clients like any other
// node inbound. Editing such a replica writes the node's inbound and refreshes
// the replica; deleting it on the node removes it there and then the replica.
//
// Nothing here talks to the node outside NodeRemoteService.Execute and the
// read calls adoption already uses, so the guard rails (enabled node, breaker,
// capabilities, rate limit) are the same as for the node detail page.

// NodeInboundService is the master side of node-targeted inbound writes.
type NodeInboundService struct {
	NodeSyncService
}

// createdByRequest remembers which replica a create request made, so a submit
// retried with the same requestId (answer lost, double click) returns that
// replica instead of failing on its now-taken tag. The node side deduplicates
// the same id on its own (api/idempotency.go).
var (
	createdMu        sync.Mutex
	createdByRequest = map[string]createdEntry{}
)

type createdEntry struct {
	replicaId uint
	at        time.Time
}

const createdTTL = 10 * time.Minute

func rememberCreated(requestId string, replicaId uint) {
	if requestId == "" {
		return
	}
	createdMu.Lock()
	defer createdMu.Unlock()
	now := time.Now()
	for k, e := range createdByRequest {
		if now.Sub(e.at) > createdTTL {
			delete(createdByRequest, k)
		}
	}
	createdByRequest[requestId] = createdEntry{replicaId: replicaId, at: now}
}

func createdFor(requestId string) (uint, bool) {
	if requestId == "" {
		return 0, false
	}
	createdMu.Lock()
	defer createdMu.Unlock()
	e, ok := createdByRequest[requestId]
	if !ok || time.Since(e.at) > createdTTL {
		return 0, false
	}
	return e.replicaId, true
}

// udpInboundTypes listen on UDP only; the rest are checked on TCP.
var udpInboundTypes = map[string]bool{"hysteria": true, "hysteria2": true, "tuic": true}

type nodeInboundHead struct {
	Id         uint   `json:"id"`
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	ListenPort int    `json:"listen_port"`
}

// prepareNodeInbound strips the master-only keys from a form payload and reads
// the fields the checks need.
func prepareNodeInbound(data json.RawMessage) (map[string]any, nodeInboundHead, error) {
	var head nodeInboundHead
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, head, common.NewError("inbound data must be a JSON object")
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, head, common.NewErrorf("inbound data: %v", err)
	}
	head.Tag = strings.TrimSpace(head.Tag)
	if head.Tag == "" {
		return nil, head, common.NewError("tag is required")
	}
	// node_id is this panel's bookkeeping, never the node's; users are bound
	// by the master (adoption + reconcile), never sent inline.
	delete(m, "node_id")
	delete(m, "users")
	return m, head, nil
}

// checkNodePort asks the node whether the port can take the inbound. A node
// without the portCheck operation (API level 1) is left to its own save.
func (s *NodeInboundService) checkNodePort(actor string, nodeId uint, head nodeInboundHead, exclude string) error {
	if head.ListenPort <= 0 {
		return nil
	}
	network := "tcp"
	if udpInboundTypes[head.Type] {
		network = "udp"
	}
	p := url.Values{}
	p.Set("port", strconv.Itoa(head.ListenPort))
	p.Set("network", network)
	if exclude != "" {
		p.Set("exclude", exclude)
	}
	raw, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{NodeId: nodeId, Op: "portCheck", Params: p})
	if err != nil {
		if strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "older 3S-UI") {
			return nil
		}
		return err
	}
	var res PortCheckResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	if res.UsedBy != "" {
		return common.NewErrorf("port %d/%s is already used on the node by inbound %q", head.ListenPort, network, res.UsedBy)
	}
	if !res.Bindable {
		detail := res.Detail
		if detail == "" {
			detail = "the node cannot bind it"
		}
		return common.NewErrorf("port %d/%s is not free on the node: %s", head.ListenPort, network, detail)
	}
	return nil
}

// CreateOnNode creates the inbound on the node and adopts it as a replica,
// binding initUsers (comma-separated client ids) like a local new inbound.
// requestId, when given, makes a retried submit a replay on the node.
func (s *NodeInboundService) CreateOnNode(actor string, nodeId uint, data json.RawMessage, initUsers, requestId string) (*model.Inbound, error) {
	if _, err := parseClientIds(initUsers); err != nil {
		return nil, err
	}
	if id, ok := createdFor(requestId); ok {
		var replica model.Inbound
		if err := database.GetDB().Model(model.Inbound{}).Where("id = ? AND node_id = ?", id, nodeId).First(&replica).Error; err == nil {
			return &replica, nil
		}
	}
	m, head, err := prepareNodeInbound(data)
	if err != nil {
		return nil, err
	}
	m["id"] = 0
	// The tag is the replica's key and unique across this panel's inbounds.
	var n int64
	if err := database.GetDB().Model(model.Inbound{}).Where("tag = ?", head.Tag).Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, common.NewErrorf("tag %q already exists on this panel", head.Tag)
	}
	if err := s.checkNodePort(actor, nodeId, head, ""); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(m)
	if _, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{
		NodeId: nodeId, Op: "save.inbounds", Object: "inbounds", Action: "new",
		Data: payload, RequestId: requestId,
	}); err != nil {
		return nil, err
	}
	if err := s.AdoptInbounds(nodeId, []string{head.Tag}, initUsers, actor); err != nil {
		return nil, common.NewErrorf("the inbound was created on the node, but adopting it failed: %v -- use Import inbounds on the Nodes page", err)
	}
	var replica model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("node_id = ? AND tag = ?", nodeId, head.Tag).First(&replica).Error; err != nil {
		return nil, err
	}
	rememberCreated(requestId, replica.Id)
	return &replica, nil
}

// loadReplica returns the replica row with the given id.
func loadReplica(id uint) (*model.Inbound, error) {
	var r model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", id).First(&r).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, common.NewErrorf("inbound %d not found", id)
		}
		return nil, err
	}
	if r.NodeId == nil {
		return nil, common.NewError("inbound is not a node replica")
	}
	return &r, nil
}

// nodeInboundId resolves the node-local id of a replica's inbound.
func (s *NodeInboundService) nodeInboundId(node *model.Node, tag string) (uint, error) {
	client := nodePushClient(node)
	defer closeIdle(client)
	m, err := s.nodeInboundTagMap(node, client)
	if err != nil {
		return 0, err
	}
	id, ok := m[tag]
	if !ok {
		return 0, common.NewErrorf("inbound %q is no longer on node %s", tag, node.Name)
	}
	return id, nil
}

// FormData answers the master's inbound form for a node: the node's TLS
// configs (an inbound's tls_id is the node's) and, for an existing replica,
// the node's own full inbound to edit.
func (s *NodeInboundService) FormData(actor string, nodeId, replicaId uint) (map[string]any, error) {
	out := map[string]any{"nodeId": nodeId}
	if replicaId > 0 {
		r, err := loadReplica(replicaId)
		if err != nil {
			return nil, err
		}
		nodeId = *r.NodeId
		out["nodeId"] = nodeId
		node, err := s.getNodeById(nodeId)
		if err != nil {
			return nil, err
		}
		rid, err := s.nodeInboundId(node, r.Tag)
		if err != nil {
			return nil, err
		}
		p := url.Values{}
		p.Set("id", strconv.FormatUint(uint64(rid), 10))
		raw, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{NodeId: nodeId, Op: "inbounds", Params: p})
		if err != nil {
			return nil, err
		}
		var payload struct {
			Inbounds []map[string]any `json:"inbounds"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Inbounds) != 1 {
			return nil, common.NewError("unexpected inbounds payload from node")
		}
		ib := payload.Inbounds[0]
		// The form edits it under the replica's id; the node's id stays here.
		ib["id"] = r.Id
		ib["node_id"] = nodeId
		out["inbound"] = ib
	}
	raw, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{NodeId: nodeId, Op: "tls"})
	if err != nil {
		return nil, err
	}
	var tls struct {
		Tls json.RawMessage `json:"tls"`
	}
	if json.Unmarshal(raw, &tls) == nil && len(tls.Tls) > 0 {
		out["tls"] = tls.Tls
	} else {
		out["tls"] = raw
	}
	return out, nil
}

// EditOnNode writes the form's edit of a replica to its node's inbound; the
// remote save refreshes the replica and its links before returning.
func (s *NodeInboundService) EditOnNode(actor string, data json.RawMessage, requestId string) (*model.Inbound, error) {
	m, head, err := prepareNodeInbound(data)
	if err != nil {
		return nil, err
	}
	r, err := loadReplica(head.Id)
	if err != nil {
		return nil, err
	}
	if head.Tag != r.Tag {
		return nil, common.NewError("the tag of a node inbound cannot be changed from the master")
	}
	node, err := s.getNodeById(*r.NodeId)
	if err != nil {
		return nil, err
	}
	rid, err := s.nodeInboundId(node, r.Tag)
	if err != nil {
		return nil, err
	}
	if err := s.checkNodePort(actor, node.Id, head, r.Tag); err != nil {
		return nil, err
	}
	m["id"] = rid
	payload, _ := json.Marshal(m)
	if _, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{
		NodeId: node.Id, Op: "save.inbounds", Object: "inbounds", Action: "edit",
		Data: payload, RequestId: requestId,
	}); err != nil {
		return nil, err
	}
	var fresh model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", r.Id).First(&fresh).Error; err != nil {
		return nil, err
	}
	return &fresh, nil
}

// DeleteOnNode deletes a replica's inbound on its node, then the replica (the
// same removal "Remove replica" does, which unbinds the master's clients).
func (s *NodeInboundService) DeleteOnNode(actor string, tag string, requestId string) error {
	var r model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("tag = ? AND node_id IS NOT NULL", tag).First(&r).Error; err != nil {
		if database.IsNotFound(err) {
			return common.NewErrorf("no node inbound %q on this panel", tag)
		}
		return err
	}
	payload, _ := json.Marshal(tag)
	if _, err := (&NodeRemoteService{}).Execute(actor, RemoteRequest{
		NodeId: *r.NodeId, Op: "save.inbounds", Object: "inbounds", Action: "del",
		Data: payload, RequestId: requestId, allowAdopted: true,
	}); err != nil {
		return err
	}
	if _, err := (&ConfigService{}).Save("inbounds", "del", payload, "", actor, ""); err != nil {
		logger.Warning("node inbound ", tag, " deleted on its node, replica removal failed: ", err)
		return common.NewErrorf("deleted on the node, but removing the replica failed: %v -- use Remove replica", err)
	}
	return nil
}
