package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/common"

	"gorm.io/gorm"
)

// Typed remote management of a node from the master.
//
// This is deliberately not a proxy. A master operator picks an operation from
// a fixed allowlist (remoteOps); each one maps to exactly one existing apiv2
// action of the node, with its parameters validated here before anything is
// sent. The node then runs it through its own services -- the same
// ConfigService.Save, InboundService and validators its own UI uses -- so
// remote and local edits share one implementation and one set of rules. There
// is no generic "call this path" and no shell.
//
// Guard rails, in the order they apply: the node must exist and be enabled;
// the op must be allowlisted; the node's capabilities must include it (API
// level 1 nodes only get the legacy subset); the circuit breaker must be
// closed; the per-node rate limit must allow it; dangerous ops need an explicit
// confirm; payloads are size-limited JSON and typed against the node's
// capabilities. Every write is audited (model.NodeOperation, no payload) and
// carries an X-Request-Id the node deduplicates on, so a retry after a lost
// response does not apply a change twice.

// RemoteRequest is one operation requested from the master UI/API.
type RemoteRequest struct {
	NodeId  uint            `json:"nodeId"`
	Op      string          `json:"op"`
	Object  string          `json:"object,omitempty"`
	Action  string          `json:"action,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Params  url.Values      `json:"-"`
	Confirm bool            `json:"confirm,omitempty"`
}

type remoteOp struct {
	method    string
	action    string // apiv2 action on the node
	write     bool   // audited; marks the node dirty on success
	legacy    bool   // available on API level 1 nodes
	dangerous bool   // needs Confirm
	timeout   time.Duration
}

const (
	remoteReadTimeout  = 10 * time.Second
	remoteWriteTimeout = 20 * time.Second
	remoteMaxPayload   = 1 << 20
	remoteMaxRetries   = 2
	remoteLogsMax      = 500
)

// remoteOps is the allowlist. Keys match NodeManagementOps on the node side.
var remoteOps = map[string]remoteOp{
	"hello":         {method: "GET", action: "nodeHello", timeout: remoteReadTimeout},
	"capabilities":  {method: "GET", action: "nodeCapabilities", timeout: remoteReadTimeout},
	"health":        {method: "GET", action: "nodeHealth", legacy: true, timeout: remoteReadTimeout}, // falls back to status on API level 1
	"services":      {method: "GET", action: "nodeServices", timeout: remoteReadTimeout},
	"logs":          {method: "GET", action: "logs", legacy: true, timeout: remoteReadTimeout},
	"inbounds":      {method: "GET", action: "inbounds", legacy: true, timeout: remoteReadTimeout},
	"outbounds":     {method: "GET", action: "outbounds", legacy: true, timeout: remoteReadTimeout},
	"endpoints":     {method: "GET", action: "endpoints", legacy: true, timeout: remoteReadTimeout},
	"services.list": {method: "GET", action: "services", legacy: true, timeout: remoteReadTimeout},
	"clients":       {method: "GET", action: "clients", legacy: true, timeout: remoteReadTimeout},
	"config":        {method: "GET", action: "config", legacy: true, timeout: remoteReadTimeout},
	"tls":           {method: "GET", action: "tls", legacy: true, timeout: remoteReadTimeout},
	"portCheck":     {method: "GET", action: "nodePortCheck", timeout: remoteReadTimeout},
	"checkOutbound": {method: "GET", action: "checkOutbound", legacy: true, timeout: remoteWriteTimeout},
	"checkConfig":   {method: "POST", action: "nodeCheckConfig", timeout: remoteWriteTimeout},

	"save.inbounds":  {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"save.outbounds": {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"save.endpoints": {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"save.services":  {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"config.route":   {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"config.dns":     {method: "POST", action: "save", write: true, legacy: true, timeout: remoteWriteTimeout},
	"core.restart":   {method: "POST", action: "restartSb", write: true, legacy: true, timeout: remoteWriteTimeout},
	"core.stop":      {method: "POST", action: "maintenance", write: true, legacy: true, dangerous: true, timeout: remoteWriteTimeout},
	"core.start":     {method: "POST", action: "maintenance", write: true, legacy: true, timeout: remoteWriteTimeout},
	"panel.restart":  {method: "POST", action: "restartApp", write: true, legacy: true, dangerous: true, timeout: remoteWriteTimeout},
}

// RemoteOpNames lists the allowlist (for the UI and docs).
func RemoteOpNames() []string {
	out := make([]string, 0, len(remoteOps))
	for k := range remoteOps {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// errRemoteRefused marks a refusal by the master's own guard rails (as opposed
// to the node failing), so the audit can tell the two apart.
var errRemoteRefused = errors.New("refused")

func refused(format string, args ...interface{}) error {
	// fmt.Errorf, not common.NewErrorf: the latter flattens %w, and the audit
	// tells refusals from failures with errors.Is.
	return fmt.Errorf("%w: "+format, append([]interface{}{errRemoteRefused}, args...)...)
}

// ---------- per-node rate limit ----------

type tokenBucket struct {
	tokens float64
	last   time.Time
}

const (
	remoteRateBurst  = 20.0
	remoteRatePerSec = 2.0
)

var (
	rateMu      sync.Mutex
	nodeBuckets = map[uint]*tokenBucket{}
)

func allowRemote(id uint) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	now := time.Now()
	b := nodeBuckets[id]
	if b == nil {
		b = &tokenBucket{tokens: remoteRateBurst, last: now}
		nodeBuckets[id] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * remoteRatePerSec
	if b.tokens > remoteRateBurst {
		b.tokens = remoteRateBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ---------- transport ----------

// nodeCall performs one apiv2 call with a context, a request id and a size
// cap, and unwraps the {success,msg,obj} envelope.
func (s *NodeService) nodeCall(ctx context.Context, n *model.Node, client *http.Client, method, action string, q url.Values, form url.Values, requestID string) (json.RawMessage, error) {
	u := nodeApiURL(n, action)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if method == "POST" {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Token", n.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", requestID)
	req.Header.Set("X-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusError(resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, nodeMaxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > nodeMaxResponseSize {
		return nil, common.NewError("node response exceeds the size limit")
	}
	var msg struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&msg); err != nil {
		return nil, common.NewError("unexpected response: not a panel apiv2 endpoint?")
	}
	if !msg.Success {
		if msg.Msg == "" {
			msg.Msg = "node rejected the request"
		}
		return nil, &nodeRefusedError{msg: msg.Msg}
	}
	return msg.Obj, nil
}

// nodeRefusedError is a well-formed refusal from the node: final, never retried.
type nodeRefusedError struct{ msg string }

func (e *nodeRefusedError) Error() string { return "node refused: " + e.msg }

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var refusedErr *nodeRefusedError
	if errors.As(err, &refusedErr) || errors.Is(err, errRemoteRefused) || errors.Is(err, context.Canceled) {
		return false
	}
	// HTTP status errors from the node are answers, not transport failures.
	return !strings.HasPrefix(err.Error(), "HTTP ")
}

// callWithRetry retries transport failures with exponential backoff and
// jitter, reusing the request id so the node's dedupe turns a retried write
// whose first answer was lost into a replay instead of a second apply.
func (s *NodeService) callWithRetry(ctx context.Context, n *model.Node, client *http.Client, method, action string, q, form url.Values, requestID string) (json.RawMessage, int, error) {
	var lastErr error
	for attempt := 0; attempt <= remoteMaxRetries; attempt++ {
		if attempt > 0 {
			wait := jitter(time.Duration(300<<uint(attempt-1)) * time.Millisecond)
			select {
			case <-ctx.Done():
				return nil, attempt, ctx.Err()
			case <-time.After(wait):
			}
		}
		obj, err := s.nodeCall(ctx, n, client, method, action, q, form, requestID)
		if err == nil {
			return obj, attempt + 1, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, attempt + 1, err
		}
	}
	return nil, remoteMaxRetries + 1, lastErr
}

// ---------- execution ----------

type NodeRemoteService struct {
	NodeService
}

func (s *NodeRemoteService) loadNode(id uint) (*model.Node, error) {
	var node model.Node
	if err := database.GetDB().Model(model.Node{}).Where("id = ?", id).First(&node).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, refused("node %d not found", id)
		}
		return nil, err
	}
	return &node, nil
}

// capsFor returns the node's capabilities, asking it once when nothing is
// cached yet -- gating a write on "unknown" would refuse everything on a panel
// that just started.
func (s *NodeRemoteService) capsFor(node *model.Node) (*NodeCapabilities, bool, error) {
	caps, legacy, known := s.GetCapabilities(node.Id)
	if known {
		return caps, legacy, nil
	}
	client := nodeHTTPClient(node)
	caps, legacy, err := s.FetchCapabilities(node, client)
	if err != nil {
		return nil, false, err
	}
	appVersion := ""
	if caps != nil {
		appVersion = caps.AppVersion
	}
	capsMu.Lock()
	nodeCapsM[node.Id] = &capsEntry{caps: caps, legacy: legacy, fetchedAt: time.Now(), forApp: appVersion}
	capsMu.Unlock()
	persistCapabilities(node.Id, caps, legacy, appVersion)
	return caps, legacy, nil
}

// checkOp applies the guard rails that do not need the payload.
func (s *NodeRemoteService) checkOp(node *model.Node, req *RemoteRequest) (remoteOp, *NodeCapabilities, error) {
	op, ok := remoteOps[req.Op]
	if !ok {
		return op, nil, refused("unknown operation %q", req.Op)
	}
	if !node.Enable {
		return op, nil, refused("node %s is disabled -- enable it first", node.Name)
	}
	if open, next := breakerOpen(node.Id); open {
		return op, nil, refused("node %s is offline (next probe %s); try again after it reconnects", node.Name, next.Format(time.RFC3339))
	}
	if op.dangerous && !req.Confirm {
		return op, nil, refused("%s needs explicit confirmation", req.Op)
	}
	caps, legacy, err := s.capsFor(node)
	if err != nil {
		return op, nil, common.NewErrorf("cannot read capabilities of %s: %v", node.Name, err)
	}
	if legacy {
		if !op.legacy {
			return op, nil, refused("node %s runs an older 3S-UI without remote management API; %s needs an upgrade of that node", node.Name, req.Op)
		}
	} else if caps != nil {
		if caps.MinMasterApi > NodeAPIVersion {
			return op, nil, refused("node %s requires a newer master", node.Name)
		}
		if !hasString(caps.Management, req.Op) {
			return op, nil, refused("node %s does not support %s", node.Name, req.Op)
		}
	}
	if !allowRemote(node.Id) {
		return op, nil, refused("too many operations on node %s; slow down", node.Name)
	}
	return op, caps, nil
}

func validJSON(data json.RawMessage) error {
	if len(data) == 0 {
		return refused("data is required")
	}
	if len(data) > remoteMaxPayload {
		return refused("data exceeds %d bytes", remoteMaxPayload)
	}
	if !json.Valid(data) {
		return refused("data is not valid JSON")
	}
	return nil
}

var logLevels = map[string]bool{"debug": true, "info": true, "notice": true, "warning": true, "error": true, "critical": true}

// buildParams validates the request and produces the node call's query/form.
func (s *NodeRemoteService) buildParams(node *model.Node, client *http.Client, req *RemoteRequest, caps *NodeCapabilities) (url.Values, url.Values, error) {
	q := url.Values{}
	form := url.Values{}
	p := req.Params
	if p == nil {
		p = url.Values{}
	}
	switch req.Op {
	case "logs":
		count := 100
		if v := p.Get("count"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return nil, nil, refused("count must be a positive number")
			}
			count = n
		}
		if count > remoteLogsMax {
			count = remoteLogsMax
		}
		level := strings.ToLower(p.Get("level"))
		if level == "" {
			level = "info"
		}
		if !logLevels[level] {
			return nil, nil, refused("unknown log level %q", level)
		}
		q.Set("c", strconv.Itoa(count))
		q.Set("l", level)
	case "inbounds", "clients":
		if id := p.Get("id"); id != "" {
			if _, err := strconv.ParseUint(id, 10, 32); err != nil {
				return nil, nil, refused("id must be a number")
			}
			q.Set("id", id)
		}
	case "portCheck":
		port, err := strconv.Atoi(p.Get("port"))
		if err != nil || port < 1 || port > 65535 {
			return nil, nil, refused("port must be 1-65535")
		}
		network := p.Get("network")
		if network != "" && network != "tcp" && network != "udp" {
			return nil, nil, refused("network must be tcp or udp")
		}
		q.Set("port", strconv.Itoa(port))
		q.Set("network", network)
		if ex := p.Get("exclude"); ex != "" {
			if len(ex) > 128 {
				return nil, nil, refused("exclude tag too long")
			}
			q.Set("exclude", ex)
		}
	case "checkOutbound":
		tag := p.Get("tag")
		if tag == "" || len(tag) > 128 {
			return nil, nil, refused("tag is required")
		}
		q.Set("tag", tag)
	case "checkConfig":
		if len(req.Data) > 0 {
			if err := validJSON(req.Data); err != nil {
				return nil, nil, err
			}
			form.Set("data", string(req.Data))
		}
	case "save.inbounds", "save.outbounds", "save.endpoints", "save.services":
		object := strings.TrimPrefix(req.Op, "save.")
		if req.Object != "" && req.Object != object {
			return nil, nil, refused("object %q does not match operation %s", req.Object, req.Op)
		}
		switch req.Action {
		case "new", "edit", "del":
		default:
			return nil, nil, refused("action must be new, edit or del")
		}
		if err := validJSON(req.Data); err != nil {
			return nil, nil, err
		}
		if err := s.checkSavePayload(node, client, object, req.Action, req.Data, caps); err != nil {
			return nil, nil, err
		}
		form.Set("object", object)
		form.Set("action", req.Action)
		form.Set("data", string(req.Data))
	case "core.stop":
		form.Set("enable", "true")
	case "core.start":
		form.Set("enable", "false")
	}
	return q, form, nil
}

// checkSavePayload types a save against the node: the protocol must be one
// the node actually runs, and an adopted inbound may not be deleted or renamed
// out from under the master's replica.
func (s *NodeRemoteService) checkSavePayload(node *model.Node, client *http.Client, object, action string, data json.RawMessage, caps *NodeCapabilities) error {
	if action == "del" {
		var tag string
		if err := json.Unmarshal(data, &tag); err != nil || tag == "" {
			return refused("del takes the tag as a JSON string")
		}
		if object == "inbounds" && s.isAdopted(node.Id, tag) {
			return refused("inbound %q is adopted by this master -- remove the replica on the Inbounds page first", tag)
		}
		return nil
	}
	var obj struct {
		Id   uint   `json:"id"`
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return refused("data must be a JSON object: %v", err)
	}
	if strings.TrimSpace(obj.Tag) == "" {
		return refused("tag is required")
	}
	if caps != nil && obj.Type != "" {
		var supported []string
		switch object {
		case "inbounds":
			supported = append(append(supported, caps.Inbounds...), caps.PanelInbounds...)
		case "outbounds":
			supported = caps.Outbounds
		case "endpoints":
			supported = caps.Endpoints
		case "services":
			supported = caps.Services
		}
		if !hasString(supported, obj.Type) {
			return refused("node %s does not support %s type %q", node.Name, strings.TrimSuffix(object, "s"), obj.Type)
		}
	}
	if object == "inbounds" && action == "edit" && obj.Id > 0 {
		// A tag change on an adopted inbound would orphan the replica.
		q := url.Values{}
		q.Set("id", strconv.FormatUint(uint64(obj.Id), 10))
		ctx, cancel := context.WithTimeout(context.Background(), remoteReadTimeout)
		defer cancel()
		raw, err := s.nodeCall(ctx, node, client, "GET", "inbounds", q, nil, newRequestID())
		if err == nil {
			var cur struct {
				Inbounds []struct {
					Tag string `json:"tag"`
				} `json:"inbounds"`
			}
			if json.Unmarshal(raw, &cur) == nil && len(cur.Inbounds) == 1 {
				old := cur.Inbounds[0].Tag
				if old != obj.Tag && s.isAdopted(node.Id, old) {
					return refused("inbound %q is adopted by this master; its tag cannot be changed remotely", old)
				}
			}
		}
	}
	return nil
}

func (s *NodeRemoteService) isAdopted(nodeId uint, tag string) bool {
	var n int64
	database.GetDB().Model(model.Inbound{}).Where("node_id = ? AND tag = ?", nodeId, tag).Count(&n)
	return n > 0
}

// Execute runs one typed operation and returns the node's answer.
func (s *NodeRemoteService) Execute(actor string, req RemoteRequest) (json.RawMessage, error) {
	node, err := s.loadNode(req.NodeId)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	requestID := newRequestID()
	op, caps, err := s.checkOp(node, &req)
	if err != nil {
		if remoteOps[req.Op].write {
			s.audit(node.Id, requestID, req, actor, started, 0, err)
		}
		return nil, err
	}

	client := nodeHTTPClient(node)
	var obj json.RawMessage
	attempts := 0
	switch req.Op {
	case "config.route", "config.dns":
		obj, attempts, err = s.saveConfigSection(node, client, req, caps, requestID)
	case "health":
		obj, attempts, err = s.health(node, client, caps, requestID)
	default:
		q, form, perr := s.buildParams(node, client, &req, caps)
		if perr != nil {
			err = perr
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), op.timeout)
		obj, attempts, err = s.callWithRetry(ctx, node, client, op.method, op.action, q, form, requestID)
		cancel()
	}
	if op.write {
		s.audit(node.Id, requestID, req, actor, started, attempts, err)
	}
	if err != nil {
		return nil, err
	}
	if req.Op == "capabilities" {
		var c NodeCapabilities
		if json.Unmarshal(obj, &c) == nil && c.ApiVersion > 0 {
			capsMu.Lock()
			nodeCapsM[node.Id] = &capsEntry{caps: &c, fetchedAt: time.Now(), forApp: c.AppVersion}
			capsMu.Unlock()
			persistCapabilities(node.Id, &c, false, c.AppVersion)
		}
	}
	if strings.HasPrefix(req.Op, "save.") || strings.HasPrefix(req.Op, "config.") {
		// The node's inbounds may have moved: let the reconcile refresh the
		// master's replicas and the subscription links built from them.
		s.markNodeDirty(node.Id)
	}
	return obj, nil
}

// health answers from nodeHealth, or from the legacy status action for an API
// level 1 node (fewer fields, same shape where they overlap).
func (s *NodeRemoteService) health(node *model.Node, client *http.Client, caps *NodeCapabilities, requestID string) (json.RawMessage, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteReadTimeout)
	defer cancel()
	if caps != nil {
		return s.callWithRetry(ctx, node, client, "GET", "nodeHealth", nil, nil, requestID)
	}
	q := url.Values{}
	q.Set("r", "cpu,mem,swp,dsk,net,sys,sbd")
	raw, attempts, err := s.callWithRetry(ctx, node, client, "GET", "status", q, nil, requestID)
	if err != nil {
		return nil, attempts, err
	}
	var st map[string]json.RawMessage
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, attempts, err
	}
	out := map[string]json.RawMessage{
		"time": json.RawMessage(strconv.FormatInt(time.Now().Unix(), 10)),
		"cpu":  st["cpu"], "mem": st["mem"], "swap": st["swp"], "disk": st["dsk"],
		"net": st["net"], "core": st["sbd"], "legacy": json.RawMessage("true"),
	}
	b, _ := json.Marshal(out)
	return b, attempts, nil
}

// saveConfigSection replaces one top-level section (route or dns) of the
// node's base config and saves it through the node's own config save, which
// validates and restarts its core. Validation runs first on API level 2 nodes
// (nodeCheckConfig), so a broken section never reaches the node's DB.
func (s *NodeRemoteService) saveConfigSection(node *model.Node, client *http.Client, req RemoteRequest, caps *NodeCapabilities, requestID string) (json.RawMessage, int, error) {
	section := strings.TrimPrefix(req.Op, "config.")
	if err := validJSON(req.Data); err != nil {
		return nil, 0, err
	}
	var sectionObj map[string]json.RawMessage
	if err := json.Unmarshal(req.Data, &sectionObj); err != nil {
		return nil, 0, refused("%s must be a JSON object", section)
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteWriteTimeout*2)
	defer cancel()
	raw, attempts, err := s.callWithRetry(ctx, node, client, "GET", "config", nil, nil, newRequestID())
	if err != nil {
		return nil, attempts, err
	}
	var wrapped struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Config == nil {
		return nil, attempts, common.NewError("node returned an unreadable config")
	}
	cfg := wrapped.Config
	cfg[section] = req.Data
	full, err := json.Marshal(cfg)
	if err != nil {
		return nil, attempts, err
	}
	if len(full) > remoteMaxPayload {
		return nil, attempts, refused("resulting config exceeds %d bytes", remoteMaxPayload)
	}
	if caps != nil {
		form := url.Values{}
		form.Set("data", string(full))
		if _, n, err := s.callWithRetry(ctx, node, client, "POST", "nodeCheckConfig", nil, form, newRequestID()); err != nil {
			return nil, attempts + n, common.NewErrorf("config check failed, nothing saved: %v", err)
		}
	}
	form := url.Values{}
	form.Set("object", "config")
	form.Set("action", "set")
	form.Set("data", string(full))
	obj, n, err := s.callWithRetry(ctx, node, client, "POST", "save", nil, form, requestID)
	return obj, attempts + n, err
}

func (s *NodeRemoteService) markNodeDirty(id uint) {
	bumpDirtyGen()
	if err := database.GetDB().Model(model.Node{}).Where("id = ?", id).Update("dirty", true).Error; err != nil {
		logger.Warning("nodes: mark dirty: ", err)
		return
	}
	afterRemoteChange(id)
}

// afterRemoteChange starts the reconcile that refreshes replicas and links
// after a remote write. A variable so tests can observe it instead of racing a
// background reconcile against their temporary database.
var afterRemoteChange = func(id uint) {
	go func() {
		if err := (&NodeSyncService{}).Reconcile(id); err != nil {
			logger.Warning("nodes: reconcile after remote change: ", err)
		}
	}()
}

const nodeOpsKeep = 10000

func (s *NodeRemoteService) audit(nodeId uint, requestID string, req RemoteRequest, actor string, started time.Time, attempts int, err error) {
	row := model.NodeOperation{
		NodeId: nodeId, RequestId: requestID, Op: req.Op, Object: req.Object, Action: req.Action,
		Actor: truncate(actor, 64), Status: "ok", Attempts: attempts,
		StartedAt: started.Unix(), DurationMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		row.Status = "failed"
		if errors.Is(err, errRemoteRefused) {
			row.Status = "refused"
		}
		row.Error = truncate(err.Error(), 512)
	}
	db := database.GetDB()
	if e := db.Create(&row).Error; e != nil {
		logger.Warning("nodes: audit remote operation: ", e)
		return
	}
	// Bounded: prune in the same write path, cheaply, every 100 rows.
	if row.Id%100 == 0 && row.Id > nodeOpsKeep {
		db.Where("id <= ?", row.Id-nodeOpsKeep).Delete(&model.NodeOperation{})
	}
	logger.Info("nodes: remote ", req.Op, " on node #", nodeId, " by ", actor, ": ", row.Status, " [", requestID, "]")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ListOperations returns a node's audit trail, newest first, paginated.
func (s *NodeRemoteService) ListOperations(nodeId uint, limit, offset int) ([]model.NodeOperation, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	db := database.GetDB().Model(model.NodeOperation{})
	if nodeId > 0 {
		db = db.Where("node_id = ?", nodeId)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.NodeOperation
	err := db.Order("id desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// ---------- token rotation ----------

// RotateToken replaces the master's token for a node without a window where
// neither works: the node mints a new token for the same admin (old still
// valid), the master verifies the new one, stores it, and only then asks the
// node -- authenticated with the new token -- to revoke the old one. If the
// verification fails, the new token is revoked with the old one and nothing
// changes on the master.
func (s *NodeRemoteService) RotateToken(actor string, nodeId uint) error {
	started := time.Now()
	req := RemoteRequest{NodeId: nodeId, Op: "token.rotate"}
	node, err := s.loadNode(nodeId)
	if err != nil {
		return err
	}
	requestID := newRequestID()
	err = s.rotateToken(node, requestID)
	s.audit(nodeId, requestID, req, actor, started, 1, err)
	return err
}

func (s *NodeRemoteService) rotateToken(node *model.Node, requestID string) error {
	if !node.Enable {
		return refused("node %s is disabled", node.Name)
	}
	caps, legacy, err := s.capsFor(node)
	if err != nil {
		return err
	}
	if legacy || caps == nil || !hasString(caps.Management, "token.rotate") {
		return refused("node %s does not support token rotation; create a new token on the node and paste it here", node.Name)
	}
	client := buildNodeHTTPClient(node)
	defer closeIdle(client)
	ctx, cancel := context.WithTimeout(context.Background(), remoteWriteTimeout)
	defer cancel()

	raw, _, err := s.callWithRetry(ctx, node, client, "POST", "nodeRotateToken", nil, url.Values{}, requestID)
	if err != nil {
		return err
	}
	var minted struct {
		Token      string `json:"token"`
		Id         uint   `json:"id"`
		PreviousId uint   `json:"previousId"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil || minted.Token == "" || minted.Id == 0 {
		return common.NewError("node returned no token")
	}
	fresh := *node
	fresh.Token = minted.Token
	if _, err := s.fetchHello(&fresh, client); err != nil {
		// Undo on the node with the still-valid old token.
		form := url.Values{}
		form.Set("id", strconv.FormatUint(uint64(minted.Id), 10))
		_, _, _ = s.callWithRetry(ctx, node, client, "POST", "nodeRevokeToken", nil, form, newRequestID())
		return common.NewErrorf("new token did not verify, rotation rolled back: %v", err)
	}
	if err := database.GetDB().Model(model.Node{}).Where("id = ?", node.Id).Update("token", minted.Token).Error; err != nil {
		return err
	}
	invalidateNodeClient(node.Id)
	if minted.PreviousId > 0 {
		form := url.Values{}
		form.Set("id", strconv.FormatUint(uint64(minted.PreviousId), 10))
		if _, _, err := s.callWithRetry(ctx, &fresh, client, "POST", "nodeRevokeToken", nil, form, newRequestID()); err != nil {
			// The master already uses the new token; the old one stays valid on
			// the node until removed there. Report it, do not undo.
			return common.NewErrorf("rotated, but the old token could not be revoked on the node (remove it there): %v", err)
		}
	}
	return nil
}

// ---------- bulk operations ----------

// BulkResult is the outcome of one node in a bulk operation.
type BulkResult struct {
	NodeId uint   `json:"nodeId"`
	Ok     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

var bulkOps = map[string]bool{
	"enable": true, "disable": true, "reconnect": true, "reconcile": true,
	"subExclude": true, "subInclude": true, "refreshCaps": true, "setGroup": true,
}

// Bulk applies a management operation to several nodes. Local state changes
// (enable, exclusion, group) commit in one transaction; network operations run
// per node with bounded parallelism and report individually.
func (s *NodeRemoteService) Bulk(actor string, ids []uint, op string, value string) ([]BulkResult, error) {
	if !bulkOps[op] {
		return nil, refused("unknown bulk operation %q", op)
	}
	if len(ids) == 0 || len(ids) > 500 {
		return nil, refused("select 1-500 nodes")
	}
	db := database.GetDB()
	results := make([]BulkResult, 0, len(ids))
	switch op {
	case "enable", "disable", "subExclude", "subInclude", "setGroup":
		col, val := "", interface{}(nil)
		switch op {
		case "enable":
			col, val = "enable", true
		case "disable":
			col, val = "enable", false
		case "subExclude":
			col, val = "sub_exclude", true
		case "subInclude":
			col, val = "sub_exclude", false
		case "setGroup":
			value = strings.TrimSpace(value)
			if len(value) > 64 {
				return nil, refused("group is limited to 64 characters")
			}
			col, val = "node_group", value
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(model.Node{}).Where("id IN ?", ids).Update(col, val).Error; err != nil {
				return err
			}
			return tx.Create(&model.Changes{
				DateTime: time.Now().Unix(), Actor: actor, Key: "nodes", Action: "bulk:" + op,
				Obj: json.RawMessage(mustJSON(map[string]interface{}{"ids": ids, "value": value})),
			}).Error
		})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if op == "enable" || op == "disable" {
				invalidateNodeClient(id)
				resetProbeBreaker(id)
			}
			results = append(results, BulkResult{NodeId: id, Ok: true})
		}
		SetLastUpdate(time.Now().Unix())
	case "reconnect", "refreshCaps":
		for _, id := range ids {
			resetProbeBreaker(id)
			forgetCapabilities(id)
			invalidateNodeClient(id)
			results = append(results, BulkResult{NodeId: id, Ok: true})
		}
		go (&NodeService{}).RefreshAll()
	case "reconcile":
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, nodeProbeParallel)
		for _, id := range ids {
			wg.Add(1)
			sem <- struct{}{}
			go func(id uint) {
				defer wg.Done()
				defer func() { <-sem }()
				err := (&NodeSyncService{}).ReconcileNow(id)
				r := BulkResult{NodeId: id, Ok: err == nil}
				if err != nil {
					r.Error = err.Error()
				}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}(id)
		}
		wg.Wait()
	}
	logger.Info("nodes: bulk ", op, " on ", len(ids), " node(s) by ", actor)
	return results, nil
}

// Reconnect clears the breaker and probes the node right away.
func (s *NodeRemoteService) Reconnect(id uint) (NodeStatus, error) {
	node, err := s.loadNode(id)
	if err != nil {
		return NodeStatus{}, err
	}
	resetProbeBreaker(id)
	forgetCapabilities(id)
	invalidateNodeClient(id)
	status := s.probe(node, nodeHTTPClient(node))
	nodeStatusMu.Lock()
	prev, had := nodeStatuses[id]
	if status.State == "online" {
		status.LastOnline = status.CheckedAt
	} else if had {
		status.LastOnline = prev.LastOnline
	}
	status = recordProbeOutcome(id, status, prev, had)
	if node.Enable {
		nodeStatuses[id] = status
	}
	nodeStatusMu.Unlock()
	if node.Enable {
		s.refreshCapabilities([]*model.Node{node}, map[uint]NodeStatus{id: status})
	}
	return decorateHealthWith(status, node.Enable, nil, getSyncState(id)), nil
}

// Detail assembles everything the node details page shows that the master
// already knows, without calling the node.
func (s *NodeRemoteService) Detail(id uint) (map[string]interface{}, error) {
	node, err := s.loadNode(id)
	if err != nil {
		return nil, err
	}
	all, err := s.GetAll()
	if err != nil {
		return nil, err
	}
	var view map[string]interface{}
	for _, v := range all {
		if v["id"] == node.Id {
			view = v
			break
		}
	}
	statuses := s.GetStatuses()
	out := map[string]interface{}{"node": view, "remoteOps": RemoteOpNames()}
	if st, ok := statuses[id]; ok {
		out["status"] = st
	} else {
		out["status"] = decorateHealthWith(NodeStatus{State: "pending"}, node.Enable, nil, getSyncState(id))
	}
	caps, legacy, known := s.GetCapabilities(id)
	out["capabilities"] = caps
	out["legacy"] = legacy
	out["capsKnown"] = known
	if e := capsError(id); e != "" {
		out["capsError"] = e
	}
	var replicas []string
	database.GetDB().Model(model.Inbound{}).Where("node_id = ?", id).Pluck("tag", &replicas)
	out["adopted"] = replicas
	ops, _, _ := s.ListOperations(id, 20, 0)
	out["operations"] = ops
	out["masterApiVersion"] = NodeAPIVersion
	return out, nil
}
