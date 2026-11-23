package service

import (
	"github.com/samur005/3s-ui/util/secretbox"

	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/service/notify"
	"github.com/samur005/3s-ui/util/common"

	"gorm.io/gorm"
)

// Live node status lives in an in-memory snapshot rebuilt on every heartbeat
// round (same pattern as onlineResources in stats.go) — a 5s cadence is not
// something we want hitting SQLite. Only online -> not-online transitions
// persist last_seen to the nodes table.

type NodeMem struct {
	Current int64 `json:"current"`
	Total   int64 `json:"total"`
}

type NodeStatus struct {
	State       string  `json:"state"` // online | offline | core-stopped
	Latency     int64   `json:"latency"`
	Cpu         float64 `json:"cpu"`
	Mem         NodeMem `json:"mem"`
	AppVersion  string  `json:"appVersion"`
	CoreVersion string  `json:"coreVersion"`
	Error       string  `json:"error,omitempty"`
	CheckedAt   int64   `json:"checkedAt"`
	LastOnline  int64   `json:"lastOnline"`

	// Extended metrics (nodeHealth.go). Zero when the node did not report
	// them; Stale marks a snapshot carried over from an earlier probe while
	// the circuit breaker holds the node back.
	Swap        NodeMem    `json:"swap"`
	Disk        NodeMem    `json:"disk"`
	Net         NodeNet    `json:"net"`
	CoreUptime  uint32     `json:"coreUptime"`
	HostUptime  uint64     `json:"hostUptime"`
	Maintenance bool       `json:"maintenance"`
	Stale       bool       `json:"stale,omitempty"`
	Failures    int        `json:"failures,omitempty"`
	NextProbeAt int64      `json:"nextProbeAt,omitempty"`
	Health      string     `json:"health"`
	Reasons     []string   `json:"reasons,omitempty"`
	ApiVersion  int        `json:"apiVersion"`
	Sync        NodeSyncSt `json:"sync"`

	onlineUsers []string
	// inboundsRev is the node's sys.inboundsRev (nodeExtras.go); not exported.
	inboundsRev     string
	onlineCheckedAt int64
}

var (
	nodeStatusMu sync.RWMutex
	nodeStatuses = map[uint]NodeStatus{}

	nodeClientMu sync.Mutex
	nodeClients  = map[uint]*http.Client{}
)

const (
	nodeProbeTimeout    = 4 * time.Second
	nodeProbeParallel   = 8
	nodeMaxResponseSize = 8 << 20

	// A custom Transport inherits none of http.DefaultTransport's pool
	// hygiene, and a zero IdleConnTimeout means an idle connection is held
	// forever. Nothing reaps the Transport either — its readLoop goroutine
	// keeps it reachable, so a dropped Transport is never collected — and the
	// node panel's own http.Server sets no IdleTimeout, so without this
	// neither end ever hangs up and every short-lived client costs one
	// permanently ESTABLISHED socket (issue #176). Short-lived clients close
	// their pool explicitly; this is the backstop for a path that forgets.
	// Value mirrors http.DefaultTransport's.
	nodeIdleConnTimeout = 90 * time.Second
)

type NodeService struct {
}

// ---------- URL / TLS helpers ----------

func normalizeWebPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "/app/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func normalizeCertPin(pin string) string {
	pin = strings.ToLower(strings.TrimSpace(pin))
	pin = strings.NewReplacer(":", "", " ", "").Replace(pin)
	return pin
}

// httpStatusError turns a node's non-200 into something diagnosable. 403 is
// almost always the node's own DomainValidator rejecting the Host we dialed:
// the panel answers only to its configured web domain, so an address entered as
// a bare IP is refused before the token is ever read — and the empty body gives
// the operator nothing to go on.
func httpStatusError(status int) error {
	if status == http.StatusForbidden {
		return common.NewError("HTTP 403 from node panel — if it has a web domain configured, its address here must use that domain, not an IP")
	}
	return common.NewErrorf("HTTP %d from node panel", status)
}

func nodeApiURL(n *model.Node, action string) string {
	return strings.TrimRight(n.BaseUrl, "/") + normalizeWebPath(n.WebPath) + "apiv2/" + action
}

// storedNodeSecret reads one node secret column (token, client_key) and opens
// it: a column scanned into a plain string bypasses SecretString.
func storedNodeSecret(tx *gorm.DB, id uint, column string) (model.SecretString, error) {
	var raw string
	if err := tx.Model(model.Node{}).Select(column).Where("id = ?", id).Find(&raw).Error; err != nil {
		return "", err
	}
	v, err := secretbox.Open(raw)
	if err != nil {
		return "", common.NewErrorf("stored %s cannot be decrypted (database moved without its secret.key?) -- enter it again", column)
	}
	return model.SecretString(v), nil
}

// validateNodeClientCert checks the optional mTLS pair parses and matches.
func validateNodeClientCert(n *model.Node) error {
	n.ClientCert = strings.TrimSpace(n.ClientCert)
	n.ClientKey = model.SecretString(strings.TrimSpace(string(n.ClientKey)))
	if n.ClientCert == "" {
		n.ClientKey = ""
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(n.BaseUrl), "https://") {
		return common.NewError("a client certificate (mTLS) needs an https:// node address")
	}
	if n.ClientKey == "" {
		return common.NewError("the client certificate needs its private key")
	}
	if _, err := tls.X509KeyPair([]byte(n.ClientCert), []byte(n.ClientKey)); err != nil {
		return common.NewErrorf("client certificate / key: %v", err)
	}
	return nil
}

func pinVerifier(pin string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return common.NewError("no peer certificate received")
		}
		sum := sha256.Sum256(rawCerts[0])
		if hex.EncodeToString(sum[:]) != pin {
			return common.NewError("certificate pin mismatch")
		}
		return nil
	}
}

func buildNodeHTTPClient(n *model.Node) *http.Client {
	// Every node client gets its own Transport now, plain http included: the
	// SSRF guard lives in the dialer (nodeNet.go), and http.DefaultTransport's
	// dialer would bypass it.
	transport := &http.Transport{
		Proxy:               nil, // a node is dialed directly, never via HTTP(S)_PROXY
		DialContext:         guardedDialContext(n.AllowPrivate, nodeProbeTimeout),
		IdleConnTimeout:     nodeIdleConnTimeout,
		TLSHandshakeTimeout: nodeProbeTimeout,
		MaxIdleConnsPerHost: 4,
	}
	if strings.HasPrefix(strings.ToLower(n.BaseUrl), "https://") {
		tlsConfig := &tls.Config{}
		if pin := normalizeCertPin(n.CertPin); pin != "" {
			// Pin replaces chain validation entirely: the leaf hash is the trust.
			tlsConfig.InsecureSkipVerify = true
			tlsConfig.VerifyPeerCertificate = pinVerifier(pin)
		} else if n.Insecure {
			tlsConfig.InsecureSkipVerify = true
		}
		// Optional mTLS: present the client certificate in addition to (never
		// instead of) verifying the node's certificate as configured above.
		if n.ClientCert != "" && n.ClientKey != "" {
			if pair, err := tls.X509KeyPair([]byte(n.ClientCert), []byte(n.ClientKey)); err == nil {
				tlsConfig.Certificates = []tls.Certificate{pair}
			} else {
				logger.Warning("nodes: client certificate of ", n.Name, ": ", err)
			}
		}
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{
		Timeout:       nodeProbeTimeout,
		Transport:     transport,
		CheckRedirect: nodeCheckRedirect,
	}
}

// closeIdle hands a node client's idle connections back.
//
// Not client.CloseIdleConnections() directly: buildNodeHTTPClient deliberately
// leaves Transport nil for a plain-http node so it shares
// http.DefaultTransport, and http.Client.CloseIdleConnections falls back to
// that same default transport when Transport is nil. Calling it there would
// empty a pool that belongs to every other caller in the process -- the update
// check, warp, cmd/setting, notify without a proxy -- once a minute, for the
// whole lifetime of one plain-http node. A shared pool needs no help from us:
// it reaps itself on its own IdleConnTimeout.
func closeIdle(client *http.Client) {
	if client == nil || client.Transport == nil || client.Transport == http.DefaultTransport {
		return
	}
	client.CloseIdleConnections()
}

func nodeHTTPClient(n *model.Node) *http.Client {
	nodeClientMu.Lock()
	defer nodeClientMu.Unlock()
	if client, ok := nodeClients[n.Id]; ok {
		return client
	}
	client := buildNodeHTTPClient(n)
	nodeClients[n.Id] = client
	return client
}

func invalidateNodeClient(id uint) {
	nodeClientMu.Lock()
	defer nodeClientMu.Unlock()
	if client, ok := nodeClients[id]; ok {
		// Dropping the map entry alone strands the Transport's idle
		// connections for nodeIdleConnTimeout; the node's address or TLS mode
		// just changed, so they are already useless.
		closeIdle(client)
		delete(nodeClients, id)
	}
}

// nodeGet calls a remote panel's apiv2 GET action and unwraps the
// {success,msg,obj} envelope (api/utils.go) into obj.
func (s *NodeService) nodeGet(n *model.Node, client *http.Client, action string, q url.Values) (json.RawMessage, error) {
	u := nodeApiURL(n, action)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Token", string(n.Token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", newRequestID())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusError(resp.StatusCode)
	}
	var msg struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, nodeMaxResponseSize)).Decode(&msg)
	if err != nil {
		return nil, common.NewError("unexpected response: not a panel apiv2 endpoint?")
	}
	if !msg.Success {
		if msg.Msg == "" {
			msg.Msg = "check the API token"
		}
		return nil, common.NewErrorf("node refused: %s", msg.Msg)
	}
	return msg.Obj, nil
}

// ---------- probing ----------

func (s *NodeService) probe(n *model.Node, client *http.Client) NodeStatus {
	start := time.Now()
	status := NodeStatus{CheckedAt: start.Unix()}
	q := url.Values{}
	q.Set("r", "cpu,mem,sys,sbd,swp,dsk,net")
	obj, err := s.nodeGet(n, client, "status", q)
	status.Latency = time.Since(start).Milliseconds()
	if err != nil {
		status.State = "offline"
		status.Error = err.Error()
		return status
	}
	var payload struct {
		Cpu float64 `json:"cpu"`
		Mem struct {
			Current int64 `json:"current"`
			Total   int64 `json:"total"`
		} `json:"mem"`
		Sys struct {
			AppVersion  string `json:"appVersion"`
			BootTime    uint64 `json:"bootTime"`
			InboundsRev string `json:"inboundsRev"`
		} `json:"sys"`
		Sbd struct {
			Running     bool   `json:"running"`
			Version     string `json:"version"`
			Maintenance bool   `json:"maintenance"`
			Stats       struct {
				Uptime uint32 `json:"Uptime"`
			} `json:"stats"`
		} `json:"sbd"`
		Swp NodeMem `json:"swp"`
		Dsk NodeMem `json:"dsk"`
		Net struct {
			Sent uint64 `json:"sent"`
			Recv uint64 `json:"recv"`
		} `json:"net"`
	}
	if err := json.Unmarshal(obj, &payload); err != nil {
		status.State = "offline"
		status.Error = "unexpected status payload"
		return status
	}
	status.Cpu = payload.Cpu
	status.Mem = NodeMem{Current: payload.Mem.Current, Total: payload.Mem.Total}
	status.AppVersion = payload.Sys.AppVersion
	status.inboundsRev = payload.Sys.InboundsRev
	status.CoreVersion = payload.Sbd.Version
	status.Swap = payload.Swp
	status.Disk = payload.Dsk
	status.Net = NodeNet{Sent: payload.Net.Sent, Recv: payload.Net.Recv}
	status.CoreUptime = payload.Sbd.Stats.Uptime
	status.Maintenance = payload.Sbd.Maintenance
	if payload.Sys.BootTime > 0 && uint64(status.CheckedAt) > payload.Sys.BootTime {
		status.HostUptime = uint64(status.CheckedAt) - payload.Sys.BootTime
	}
	if payload.Sbd.Running {
		status.State = "online"
		obj, err := s.nodeGet(n, client, "onlines", nil)
		if err != nil {
			logger.Warning("nodes: get onlines from ", n.Name, ": ", err)
			return status
		}
		var online onlines
		if err := json.Unmarshal(obj, &online); err != nil {
			logger.Warning("nodes: decode onlines from ", n.Name, ": ", err)
			return status
		}
		status.onlineUsers = online.User
		status.onlineCheckedAt = time.Now().Unix()
	} else {
		status.State = "core-stopped"
	}
	return status
}

// RefreshAll probes every enabled node with bounded parallelism and atomically
// swaps in a freshly built snapshot. Called by the @every 5s cron job.
func (s *NodeService) RefreshAll() {
	loadBreakerState()
	var nodes []*model.Node
	db := database.GetDB()
	err := db.Model(model.Node{}).Where("enable = ?", true).Find(&nodes).Error
	if err != nil {
		logger.Warning("nodes: load for refresh failed: ", err)
		return
	}

	newStatuses := make(map[uint]NodeStatus, len(nodes))
	if len(nodes) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, nodeProbeParallel)
		now := time.Now()
		for _, n := range nodes {
			// Circuit breaker: a node that keeps failing is probed on an
			// exponential schedule instead of every 5s. Its last snapshot is
			// carried over, marked stale, so the UI never shows it as fresh.
			if held, prev := probeHeld(n.Id, now); held {
				mu.Lock()
				newStatuses[n.Id] = prev
				mu.Unlock()
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(n *model.Node) {
				defer wg.Done()
				defer func() { <-sem }()
				status := s.probe(n, nodeHTTPClient(n))
				mu.Lock()
				newStatuses[n.Id] = status
				mu.Unlock()
			}(n)
		}
		wg.Wait()
	}

	nodeStatusMu.Lock()
	old := nodeStatuses
	for id, status := range newStatuses {
		prev, had := old[id]
		if status.Stale {
			continue // carried over as-is by the breaker
		}
		if status.State == "online" {
			status.LastOnline = status.CheckedAt
		} else if had {
			status.LastOnline = prev.LastOnline
		}
		newStatuses[id] = recordProbeOutcome(id, status, prev, had)
	}
	nodeStatuses = newStatuses
	nodeStatusMu.Unlock()

	s.refreshCapabilities(nodes, newStatuses)

	// A node whose inbounds changed on its own panel: refresh its replicas
	// and links now rather than at the hourly reconcile.
	for _, n := range nodes {
		if st, ok := newStatuses[n.Id]; ok && !st.Stale && st.State != "offline" {
			noteInboundsRevision(n, st.inboundsRev)
		}
	}

	// Persist last_seen only on online -> not-online transitions. `old` was
	// swapped out under the lock, nothing writes it anymore.
	for id, status := range newStatuses {
		prev, had := old[id]
		if had && prev.State == "online" && status.State != "online" && prev.LastOnline > 0 {
			err = db.Model(model.Node{}).Where("id = ?", id).Update("last_seen", prev.LastOnline).Error
			if err != nil {
				logger.Warning("nodes: persist last_seen failed: ", err)
			}
		}
	}

	s.notifyStatuses(nodes, newStatuses)
}

// nodeFailStreak counts consecutive non-online probes per node.
//
// Debouncing has to happen here rather than in the notifier, which sees events
// but not the probe cadence behind them: at 5s a single dropped packet turns
// into a down alert and an up alert a few seconds later, and a node on a
// lossy link would do that all day. Guarded by nodeStatusMu, the same lock the
// snapshot uses -- RefreshAll is the only writer, and NodesJob's TryLock keeps
// two passes from overlapping anyway.
var nodeFailStreak = map[uint]int{}

// notifyStatuses turns this pass's probe results into node events.
//
// It publishes unconditionally on the up side and past the streak threshold on
// the down side; deciding whether either is *new* is the suppressor's job, so
// the two layers do not have to agree on what "already reported" means.
func (s *NodeService) notifyStatuses(nodes []*model.Node, statuses map[uint]NodeStatus) {
	if len(statuses) == 0 {
		return
	}
	var settingService SettingService
	flap := settingService.GetNotifyThresholds().NodeFlap
	if flap < 1 {
		flap = 1
	}
	// Asked once for the pass rather than left to Publish, which reads the
	// settings per event: this loop runs every five seconds and once per node,
	// so on a panel with the node alerts off that was a settings scan per node
	// per pass to decide nothing. The streak bookkeeping below still runs, so
	// switching the alerts on does not inherit a stale count.
	wanted := settingService.NotifyWants(notify.NodeUp, notify.NodeDown)

	names := make(map[uint]string, len(nodes))
	for _, n := range nodes {
		names[n.Id] = n.Name
	}

	nodeStatusMu.Lock()
	defer nodeStatusMu.Unlock()
	// Drop streaks for nodes that are gone or disabled, so the map cannot grow
	// past the node count.
	for id := range nodeFailStreak {
		if _, live := statuses[id]; !live {
			delete(nodeFailStreak, id)
		}
	}

	for id, status := range statuses {
		name := names[id]
		if name == "" {
			name = "#" + strconv.FormatUint(uint64(id), 10)
		}
		if status.State == "online" {
			delete(nodeFailStreak, id)
			if wanted {
				notify.Publish(notify.Event{
					Kind:    notify.NodeUp,
					Subject: name,
					Data:    &notify.NodeData{LatencyMs: status.Latency},
				})
			}
			continue
		}
		nodeFailStreak[id]++
		if nodeFailStreak[id] < flap || !wanted {
			continue
		}
		notify.Publish(notify.Event{
			Kind:    notify.NodeDown,
			Subject: name,
			Data:    &notify.NodeData{Err: status.Error},
		})
	}
}

// GetStatuses returns a copy of the live snapshot for API responses.
func (s *NodeService) GetStatuses() map[uint]NodeStatus {
	nodeStatusMu.RLock()
	out := make(map[uint]NodeStatus, len(nodeStatuses))
	for id, status := range nodeStatuses {
		out[id] = status
	}
	nodeStatusMu.RUnlock()
	// Health is derived at read time from the probe, the sync state and the
	// cached capabilities, so it is never older than its inputs.
	for id, status := range out {
		out[id] = decorateHealth(id, status)
	}
	return out
}

// TestNode probes the node described by the (possibly unsaved) form data with
// a one-off client, so Test Connection reflects the form, not the DB row. An
// empty token on an existing node falls back to the stored one.
func (s *NodeService) TestNode(data json.RawMessage) (NodeStatus, error) {
	var node model.Node
	err := json.Unmarshal(data, &node)
	if err != nil {
		return NodeStatus{}, err
	}
	node.BaseUrl, err = validateNodeBaseURL(node.BaseUrl)
	if err != nil {
		return NodeStatus{}, err
	}
	if node.Token == "" && node.Id > 0 {
		node.Token, err = storedNodeSecret(database.GetDB(), node.Id, "token")
		if err != nil {
			return NodeStatus{}, err
		}
	}
	if node.ClientCert != "" && node.ClientKey == "" && node.Id > 0 {
		node.ClientKey, err = storedNodeSecret(database.GetDB(), node.Id, "client_key")
		if err != nil {
			return NodeStatus{}, err
		}
	}
	client := buildNodeHTTPClient(&node)
	defer closeIdle(client)
	status := s.probe(&node, client)
	// Report the API level too, so the form can say up front whether the
	// node supports remote management or only the legacy sync.
	if status.State != "offline" {
		if hello, err := s.fetchHello(&node, client); err == nil {
			status.ApiVersion = hello.ApiVersion
		} else {
			status.ApiVersion = 1
		}
	}
	return decorateHealthWith(status, true, nil, NodeSyncSt{}), nil
}

// ---------- CRUD ----------

// GetAll returns nodes in panel shape. The token never leaves the server —
// only a tokenSet flag does.
func (s *NodeService) GetAll() ([]map[string]interface{}, error) {
	var nodes []*model.Node
	db := database.GetDB()
	err := db.Model(model.Node{}).Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]interface{}{
			"id":       n.Id,
			"enable":   n.Enable,
			"name":     n.Name,
			"baseUrl":  n.BaseUrl,
			"webPath":  n.WebPath,
			"insecure": n.Insecure,
			"certPin":  n.CertPin,
			"desc":     n.Desc,
			"lastSeen": n.LastSeen,
			"tokenSet": n.Token != "",
			"dirty":    n.Dirty,
			"lastSync": n.LastSync,
			"syncMode": n.SyncMode,
			// multi-node management
			"group":        n.NodeGroup,
			"location":     n.Location,
			"allowPrivate": n.AllowPrivate,
			"subExclude":   n.SubExclude,
			"capsAt":       n.CapsAt,
			// mTLS: the certificate is public, the key never leaves.
			"clientCert":   n.ClientCert,
			"clientKeySet": n.ClientKey != "",
		})
	}
	return out, nil
}

// redactNodeToken masks a plaintext token in the audit payload before it is
// written to the Changes table, which flows back to the UI via GET
// api/changes. The del payload is a bare id — returned untouched.
func redactNodeToken(data json.RawMessage) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}
	if token, ok := m["token"].(string); ok && token != "" {
		m["token"] = "***"
		if redacted, err := json.Marshal(m); err == nil {
			return redacted
		}
	}
	return data
}

func (s *NodeService) Save(tx *gorm.DB, act string, data json.RawMessage) error {
	_, err := s.save(tx, act, data)
	return err
}

// SaveWithLinks is Save for callers that own the transaction: the returned
// func, run after the commit, regenerates the node's aggregated subscription
// links from the stored replica rows right away -- a rename or any other edit
// shows in every bound client's subscription immediately instead of after the
// next reconcile, and it works while the node is offline (no node call).
func (s *NodeService) SaveWithLinks(tx *gorm.DB, act string, data json.RawMessage) (func(), error) {
	id, err := s.save(tx, act, data)
	if err != nil || act == "del" || id == 0 {
		return nil, err
	}
	return func() { RefreshNodeLinksFromReplicas(id) }, nil
}

// RefreshNodeLinksFromReplicas rebuilds node id's "[name] " links in every
// client from the master's replica rows, without contacting the node.
func RefreshNodeLinksFromReplicas(id uint) {
	db := database.GetDB()
	if db == nil {
		return
	}
	var node model.Node
	if err := db.Model(model.Node{}).Where("id = ?", id).First(&node).Error; err != nil {
		return
	}
	(&NodeSyncService{}).refreshNodeLinks(&node)
}

func (s *NodeService) save(tx *gorm.DB, act string, data json.RawMessage) (uint, error) {
	var err error
	var savedId uint

	switch act {
	case "new", "edit":
		var node model.Node
		err = json.Unmarshal(data, &node)
		if err != nil {
			return 0, err
		}
		node.Name = strings.TrimSpace(node.Name)
		if node.Name == "" {
			return 0, common.NewError("node name is required")
		}
		node.BaseUrl, err = validateNodeBaseURL(node.BaseUrl)
		if err != nil {
			return 0, err
		}
		node.WebPath = normalizeWebPath(node.WebPath)
		node.NodeGroup = strings.TrimSpace(node.NodeGroup)
		node.Location = strings.TrimSpace(node.Location)
		if len(node.NodeGroup) > 64 || len(node.Location) > 64 {
			return 0, common.NewError("node group and location are limited to 64 characters")
		}
		node.CertPin = normalizeCertPin(node.CertPin)
		switch node.SyncMode {
		case "", "one_way":
			node.SyncMode = "one_way"
		case "bidirectional":
			// ok — opt-in only via explicit save
		default:
			return 0, common.NewError("unknown syncMode; use one_way or bidirectional")
		}
		var oldName string
		if act == "edit" {
			if node.Id == 0 {
				return 0, common.NewError("node id is required")
			}
			// Empty token on edit means "keep the stored one" — the UI never
			// sees the token, so it cannot send it back.
			if node.Token == "" {
				node.Token, err = storedNodeSecret(tx, node.Id, "token")
				if err != nil {
					return 0, err
				}
			}
			// Same for the mTLS key; clearing the certificate clears both.
			if node.ClientCert != "" && node.ClientKey == "" {
				node.ClientKey, err = storedNodeSecret(tx, node.Id, "client_key")
				if err != nil {
					return 0, err
				}
			}
			// last_seen is heartbeat-owned; don't let a stale form value clobber it.
			var oldNode struct {
				Name     string
				LastSeen int64
			}
			err = tx.Model(model.Node{}).Select("name", "last_seen").Where("id = ?", node.Id).Find(&oldNode).Error
			if err != nil {
				return 0, err
			}
			oldName = oldNode.Name
			node.LastSeen = oldNode.LastSeen
		}
		if node.Token == "" {
			return 0, common.NewError("node API token is required")
		}
		if err = validateNodeClientCert(&node); err != nil {
			return 0, err
		}
		// The name is the aggregated-link marker (see nodeLinkPrefix), so a
		// bracket in it would make one node's prefix match another's links — the
		// two would then strip and re-add each other's every reconcile. Only
		// enforced on the name actually being introduced: a node created before
		// this rule must stay editable (token, baseUrl, enable) without being
		// forced through a rename it did not ask for.
		if (act == "new" || oldName != node.Name) && strings.ContainsAny(node.Name, "[]") {
			return 0, common.NewError("node name cannot contain [ or ]")
		}
		// Capabilities are heartbeat-owned and never part of the form; a plain
		// Save would write their zero values over the cached answer.
		if act == "edit" {
			err = tx.Omit("capabilities", "caps_at", "breaker_fails", "breaker_until").Save(&node).Error
		} else {
			err = tx.Save(&node).Error
		}
		if err != nil {
			return 0, err
		}
		// A rename would orphan the "[old] " aggregated links (refreshNodeLinks
		// keys the prefix on the current name); carry them over in place.
		if oldName != "" && oldName != node.Name {
			if err = renameNodeLinkPrefix(tx, oldName, node.Name); err != nil {
				return 0, err
			}
		}
		invalidateNodeClient(node.Id)
		resetProbeBreaker(node.Id)
		forgetCapabilities(node.Id)
		savedId = node.Id
	case "del":
		var id uint
		err = json.Unmarshal(data, &id)
		if err != nil {
			return 0, err
		}
		// Refuse while replicas exist: deleting the node would strand them as
		// uneditable orphan rows and leave their "[name] " links in every
		// client forever (refreshNodeLinks never runs for a vanished node).
		// Same loud-failure stance as the adoption tag-collision check.
		var replicas int64
		err = tx.Model(model.Inbound{}).Where("node_id = ?", id).Count(&replicas).Error
		if err != nil {
			return 0, err
		}
		if replicas > 0 {
			return 0, common.NewErrorf("node still has %d adopted inbound(s) — remove them on the Inbounds page first", replicas)
		}
		err = tx.Where("id = ?", id).Delete(model.Node{}).Error
		if err != nil {
			return 0, err
		}
		invalidateNodeClient(id)
		nodeStatusMu.Lock()
		delete(nodeStatuses, id)
		nodeStatusMu.Unlock()
		resetProbeBreaker(id)
		forgetCapabilities(id)
		forgetSyncState(id)
		forgetInboundsRevision(id)
	default:
		return 0, common.NewErrorf("unknown action: %s", act)
	}
	return savedId, nil
}

// renameNodeLinkPrefix rewrites the "[old] " prefix on aggregated node links to
// "[new] " across every client, so renaming a node doesn't orphan the external
// links refreshNodeLinks emits under the node name. Runs in the node-save tx.
func renameNodeLinkPrefix(tx *gorm.DB, oldName, newName string) error {
	oldPrefix := nodeLinkPrefix(oldName)
	newPrefix := nodeLinkPrefix(newName)
	var clients []model.Client
	if err := tx.Model(model.Client{}).Find(&clients).Error; err != nil {
		return err
	}
	for i := range clients {
		c := &clients[i]
		var links []map[string]string
		if err := json.Unmarshal(c.Links, &links); err != nil {
			continue
		}
		changed := false
		for _, l := range links {
			if strings.HasPrefix(l["remark"], oldPrefix) {
				l["remark"] = newPrefix + strings.TrimPrefix(l["remark"], oldPrefix)
				changed = true
			}
		}
		if !changed {
			continue
		}
		newLinks, err := json.MarshalIndent(links, "", "  ")
		if err != nil {
			continue
		}
		if err := tx.Model(model.Client{}).Where("id = ?", c.Id).Update("links", newLinks).Error; err != nil {
			return err
		}
	}
	return nil
}
