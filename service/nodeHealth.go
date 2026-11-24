package service

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
)

// Master-side node health: the circuit breaker on the heartbeat, rates from
// consecutive probes, the capabilities cache, the per-node sync outcome, and
// the derived health state the UI shows (online, offline, degraded,
// incompatible, syncing, disabled).

// NodeNet is the node's interface counters and the rate derived from the last
// two fresh probes (bytes per second; 0 until two probes exist).
type NodeNet struct {
	Sent uint64 `json:"sent"`
	Recv uint64 `json:"recv"`
	Up   uint64 `json:"up"`
	Down uint64 `json:"down"`
}

// NodeSyncSt is the outcome of the last client reconcile for a node.
type NodeSyncSt struct {
	Running     bool   `json:"running"`
	LastAttempt int64  `json:"lastAttempt"`
	LastOk      int64  `json:"lastOk"`
	LastError   string `json:"lastError,omitempty"`
	Failures    int    `json:"failures"`
	NextRetryAt int64  `json:"nextRetryAt,omitempty"`
}

const (
	// Probes after this many consecutive offline results move to the
	// exponential schedule.
	breakerThreshold = 3
	breakerBase      = 5 * time.Second
	breakerMax       = 2 * time.Minute

	capsMaxAge      = 30 * time.Minute
	capsRetryAfter  = time.Minute
	capsFetchPar    = 4
	degradedLatency = 1500 // ms
	degradedPercent = 95.0
	syncDegradedAt  = 3 // consecutive reconcile failures

	syncBackoffMax = 15 * time.Minute
)

// ---------- circuit breaker ----------

var (
	breakerMu     sync.Mutex
	nodeFails     = map[uint]int{}
	nodeNextProbe = map[uint]time.Time{}
)

// jitter spreads d by ±20% so a cluster that failed together does not retry
// together.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := int64(d) / 5
	return d + time.Duration(rand.Int64N(2*span+1)-span)
}

// breakerDelay is the wait before the next probe after fails consecutive
// failures: none below the threshold, then 5s, 10s, 20s ... capped at 2min.
func breakerDelay(fails int) time.Duration {
	if fails < breakerThreshold {
		return 0
	}
	d := breakerBase
	for i := breakerThreshold; i < fails && d < breakerMax; i++ {
		d *= 2
	}
	if d > breakerMax {
		d = breakerMax
	}
	return d
}

// probeHeld reports whether the breaker holds this node back this round, and
// if so the snapshot to carry over (marked stale).
func probeHeld(id uint, now time.Time) (bool, NodeStatus) {
	breakerMu.Lock()
	next, ok := nodeNextProbe[id]
	fails := nodeFails[id]
	breakerMu.Unlock()
	if !ok || !now.Before(next) {
		return false, NodeStatus{}
	}
	nodeStatusMu.RLock()
	prev, had := nodeStatuses[id]
	nodeStatusMu.RUnlock()
	if !had {
		// Breaker restored from the database after a restart: there is no
		// snapshot to carry over, so hold the node as offline until its
		// scheduled probe instead of probing at full rate.
		prev = NodeStatus{State: "offline", CheckedAt: now.Unix(), Error: "backing off after repeated failures"}
	}
	prev.Stale = true
	prev.Failures = fails
	prev.NextProbeAt = next.Unix()
	return true, prev
}

// recordProbeOutcome advances the breaker on a fresh probe and fills the
// derived fields (failures, next probe, network rate).
func recordProbeOutcome(id uint, status, prev NodeStatus, had bool) NodeStatus {
	breakerMu.Lock()
	persist := false
	var pFails int
	var pUntil int64
	if status.State == "offline" {
		nodeFails[id]++
		fails := nodeFails[id]
		if d := breakerDelay(fails); d > 0 {
			next := time.Now().Add(jitter(d))
			nodeNextProbe[id] = next
			status.NextProbeAt = next.Unix()
			persist, pFails, pUntil = true, fails, next.Unix()
		}
		status.Failures = fails
	} else {
		if nodeFails[id] >= breakerThreshold || breakerPersisted[id] {
			persist = true
		}
		delete(nodeFails, id)
		delete(nodeNextProbe, id)
	}
	if persist {
		breakerPersisted[id] = pFails > 0
	}
	breakerMu.Unlock()
	if persist {
		persistBreaker(id, pFails, pUntil)
	}

	if had && prev.State != "offline" && status.State != "offline" && status.CheckedAt > prev.CheckedAt &&
		prev.Net.Sent > 0 && status.Net.Sent >= prev.Net.Sent && status.Net.Recv >= prev.Net.Recv {
		dt := uint64(status.CheckedAt - prev.CheckedAt)
		status.Net.Up = (status.Net.Sent - prev.Net.Sent) / dt
		status.Net.Down = (status.Net.Recv - prev.Net.Recv) / dt
	}
	return status
}

// resetProbeBreaker lets the next heartbeat probe the node at once (manual
// reconnect, an edit of its address or token).
func resetProbeBreaker(id uint) {
	breakerMu.Lock()
	was := breakerPersisted[id]
	delete(nodeFails, id)
	delete(nodeNextProbe, id)
	delete(breakerPersisted, id)
	breakerMu.Unlock()
	if was {
		persistBreaker(id, 0, 0)
	}
}

// breakerPersisted marks nodes whose open breaker is stored in the nodes row,
// so recovery clears the row once and healthy nodes never write.
var (
	breakerPersisted = map[uint]bool{}
	breakerLoadOnce  sync.Once
)

// persistBreaker stores the breaker so a master restart keeps backing off a
// node that was failing instead of probing it at full rate again.
func persistBreaker(id uint, fails int, until int64) {
	db := database.GetDB()
	if db == nil {
		return
	}
	if err := db.Model(model.Node{}).Where("id = ?", id).
		Updates(map[string]any{"breaker_fails": fails, "breaker_until": until}).Error; err != nil {
		logger.Warning("nodes: persist breaker state: ", err)
	}
}

// loadBreakerState restores the persisted breaker on the first heartbeat.
func loadBreakerState() {
	breakerLoadOnce.Do(func() {
		db := database.GetDB()
		if db == nil {
			return
		}
		var rows []struct {
			Id           uint
			BreakerFails int
			BreakerUntil int64
		}
		if err := db.Model(model.Node{}).Select("id", "breaker_fails", "breaker_until").
			Where("breaker_fails > 0").Find(&rows).Error; err != nil {
			return
		}
		breakerMu.Lock()
		for _, r := range rows {
			nodeFails[r.Id] = r.BreakerFails
			if r.BreakerUntil > 0 {
				nodeNextProbe[r.Id] = time.Unix(r.BreakerUntil, 0)
			}
			breakerPersisted[r.Id] = true
		}
		breakerMu.Unlock()
	})
}

// breakerOpen reports whether remote operations should fail fast: the node is
// past the threshold and waiting for its next scheduled probe.
func breakerOpen(id uint) (bool, time.Time) {
	breakerMu.Lock()
	defer breakerMu.Unlock()
	next, ok := nodeNextProbe[id]
	if !ok || nodeFails[id] < breakerThreshold || !time.Now().Before(next) {
		return false, time.Time{}
	}
	return true, next
}

// ---------- sync outcome ----------

var (
	syncStMu     sync.Mutex
	nodeSyncStat = map[uint]NodeSyncSt{}
)

func syncStarted(id uint) {
	syncStMu.Lock()
	st := nodeSyncStat[id]
	st.Running = true
	st.LastAttempt = time.Now().Unix()
	nodeSyncStat[id] = st
	syncStMu.Unlock()
}

func syncFinished(id uint, err error) {
	syncStMu.Lock()
	st := nodeSyncStat[id]
	st.Running = false
	if err == nil {
		st.LastOk = time.Now().Unix()
		st.LastError = ""
		st.Failures = 0
		st.NextRetryAt = 0
	} else {
		st.LastError = err.Error()
		st.Failures++
		st.NextRetryAt = time.Now().Add(syncBackoffFor(st.Failures)).Unix()
	}
	nodeSyncStat[id] = st
	syncStMu.Unlock()
}

// syncBackoffFor is the background reconcile backoff after fails consecutive
// failures: the fixed 30s floor, doubled per failure, capped at 15 minutes.
// Interactive reconciles ignore it.
func syncBackoffFor(fails int) time.Duration {
	d := reconcileBackoff
	for i := 0; i < fails && d < syncBackoffMax; i++ {
		d *= 2
	}
	if d > syncBackoffMax {
		d = syncBackoffMax
	}
	return d
}

func nodeSyncFailures(id uint) int {
	syncStMu.Lock()
	defer syncStMu.Unlock()
	return nodeSyncStat[id].Failures
}

func getSyncState(id uint) NodeSyncSt {
	syncStMu.Lock()
	defer syncStMu.Unlock()
	return nodeSyncStat[id]
}

func forgetSyncState(id uint) {
	syncStMu.Lock()
	delete(nodeSyncStat, id)
	syncStMu.Unlock()
}

// ---------- capabilities cache ----------

type capsEntry struct {
	caps      *NodeCapabilities // nil for a legacy (API level 1) node
	legacy    bool
	fetchedAt time.Time
	forApp    string // the appVersion the answer belongs to
	err       string
	errAt     time.Time
	inflight  bool
}

var (
	capsMu    sync.Mutex
	nodeCapsM = map[uint]*capsEntry{}
	capsSem   = make(chan struct{}, capsFetchPar)
)

func forgetCapabilities(id uint) {
	capsMu.Lock()
	delete(nodeCapsM, id)
	capsMu.Unlock()
}

// isUnknownAction recognises the answer an older node gives to an apiv2
// action it does not have: the API level 1 signal.
func isUnknownAction(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown action")
}

func (s *NodeService) fetchHello(n *model.Node, client *http.Client) (*NodeHello, error) {
	obj, err := s.nodeGet(n, client, "nodeHello", nil)
	if err != nil {
		return nil, err
	}
	var hello NodeHello
	if err := json.Unmarshal(obj, &hello); err != nil {
		return nil, err
	}
	return &hello, nil
}

// FetchCapabilities asks the node for its capabilities now. legacy is true for
// a node that predates the API (no nodeCapabilities action).
func (s *NodeService) FetchCapabilities(n *model.Node, client *http.Client) (*NodeCapabilities, bool, error) {
	obj, err := s.nodeGet(n, client, "nodeCapabilities", nil)
	if isUnknownAction(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	var caps NodeCapabilities
	if err := json.Unmarshal(obj, &caps); err != nil {
		return nil, false, err
	}
	return &caps, false, nil
}

// refreshCapabilities keeps the cache current for every node that answered
// this round: first contact, a changed panel version (an upgrade changes what
// it can do), or an answer older than capsMaxAge. Fetches run in the
// background, bounded, one per node at a time; the heartbeat never waits.
func (s *NodeService) refreshCapabilities(nodes []*model.Node, statuses map[uint]NodeStatus) {
	now := time.Now()
	for _, n := range nodes {
		st, ok := statuses[n.Id]
		if !ok || st.Stale || st.State == "offline" {
			continue
		}
		capsMu.Lock()
		e := nodeCapsM[n.Id]
		if e == nil {
			e = &capsEntry{}
			// Seed from the stored answer so a panel restart does not refetch
			// every node at once.
			if n.CapsAt > 0 && len(n.Capabilities) > 0 {
				var caps NodeCapabilities
				if json.Unmarshal(n.Capabilities, &caps) == nil {
					if caps.ApiVersion <= 1 {
						e.legacy = true
					} else {
						e.caps = &caps
					}
					e.forApp = caps.AppVersion
					e.fetchedAt = time.Unix(n.CapsAt, 0)
				}
			}
			nodeCapsM[n.Id] = e
		}
		need := !e.inflight && (e.fetchedAt.IsZero() || e.forApp != st.AppVersion || now.Sub(e.fetchedAt) > capsMaxAge)
		if need && e.err != "" && now.Sub(e.errAt) < capsRetryAfter {
			need = false
		}
		if need {
			e.inflight = true
		}
		capsMu.Unlock()
		if !need {
			continue
		}
		node := *n
		appVersion := st.AppVersion
		go func() {
			capsSem <- struct{}{}
			defer func() { <-capsSem }()
			caps, legacy, err := s.FetchCapabilities(&node, nodeHTTPClient(&node))
			capsMu.Lock()
			e := nodeCapsM[node.Id]
			if e == nil { // node deleted or edited meanwhile
				capsMu.Unlock()
				return
			}
			e.inflight = false
			if err != nil {
				e.err, e.errAt = err.Error(), time.Now()
				capsMu.Unlock()
				logger.Warning("nodes: capabilities of ", node.Name, ": ", err)
				return
			}
			e.err = ""
			e.caps, e.legacy, e.fetchedAt, e.forApp = caps, legacy, time.Now(), appVersion
			capsMu.Unlock()
			persistCapabilities(node.Id, caps, legacy, appVersion)
		}()
	}
}

func persistCapabilities(id uint, caps *NodeCapabilities, legacy bool, appVersion string) {
	var stored any = caps
	if legacy || caps == nil {
		stored = map[string]any{"apiVersion": 1, "appVersion": appVersion}
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return
	}
	db := database.GetDB()
	if db == nil { // async fetch finishing after shutdown
		return
	}
	err = db.Model(model.Node{}).Where("id = ?", id).
		Updates(map[string]any{"capabilities": json.RawMessage(raw), "caps_at": time.Now().Unix()}).Error
	if err != nil {
		logger.Warning("nodes: persist capabilities: ", err)
	}
}

// GetCapabilities returns the cached capabilities. known is false when the
// node has not been asked yet; legacy is true for an API level 1 node.
func (s *NodeService) GetCapabilities(id uint) (caps *NodeCapabilities, legacy bool, known bool) {
	capsMu.Lock()
	e := nodeCapsM[id]
	capsMu.Unlock()
	if e != nil && !e.fetchedAt.IsZero() {
		return e.caps, e.legacy, true
	}
	// Not in memory (panel just started, node not probed yet): the DB copy.
	var n model.Node
	if err := database.GetDB().Model(model.Node{}).Select("id", "capabilities", "caps_at").Where("id = ?", id).First(&n).Error; err != nil {
		return nil, false, false
	}
	if n.CapsAt == 0 || len(n.Capabilities) == 0 {
		return nil, false, false
	}
	var c NodeCapabilities
	if json.Unmarshal(n.Capabilities, &c) != nil {
		return nil, false, false
	}
	if c.ApiVersion <= 1 {
		return nil, true, true
	}
	return &c, false, true
}

func capsError(id uint) string {
	capsMu.Lock()
	defer capsMu.Unlock()
	if e := nodeCapsM[id]; e != nil {
		return e.err
	}
	return ""
}

// ---------- derived health ----------

func decorateHealth(id uint, status NodeStatus) NodeStatus {
	caps, legacy, known := (&NodeService{}).GetCapabilities(id)
	var ce *capsEntry
	if known {
		ce = &capsEntry{caps: caps, legacy: legacy}
	}
	if err := capsError(id); err != "" {
		if ce == nil {
			ce = &capsEntry{}
		}
		ce.err = err
	}
	return decorateHealthWith(status, true, ce, getSyncState(id))
}

func pctOf(m NodeMem) float64 {
	if m.Total <= 0 {
		return 0
	}
	return float64(m.Current) / float64(m.Total) * 100
}

// decorateHealthWith derives Health and its reasons. Order is precedence:
// disabled, offline, incompatible, syncing, degraded, online.
func decorateHealthWith(status NodeStatus, enabled bool, ce *capsEntry, sync NodeSyncSt) NodeStatus {
	status.Sync = sync
	status.Reasons = nil
	if ce != nil {
		switch {
		case ce.caps != nil:
			status.ApiVersion = ce.caps.ApiVersion
		case ce.legacy:
			status.ApiVersion = 1
		}
	}
	switch {
	case !enabled:
		status.Health = "disabled"
		return status
	case status.State == "offline":
		status.Health = "offline"
		if status.Error != "" {
			status.Reasons = append(status.Reasons, status.Error)
		}
		return status
	}
	if ce != nil && ce.caps != nil && ce.caps.MinMasterApi > NodeAPIVersion {
		status.Health = "incompatible"
		status.Reasons = append(status.Reasons, "node requires a newer master (node management API level "+itoa(ce.caps.MinMasterApi)+", this master speaks "+itoa(NodeAPIVersion)+")")
		return status
	}
	if sync.Running {
		status.Health = "syncing"
		return status
	}
	if status.State == "core-stopped" {
		if status.Maintenance {
			status.Reasons = append(status.Reasons, "sing-box stopped for maintenance")
		} else {
			status.Reasons = append(status.Reasons, "sing-box is not running")
		}
	}
	if status.Latency > degradedLatency {
		status.Reasons = append(status.Reasons, "high latency ("+itoa(int(status.Latency))+" ms)")
	}
	if pctOf(status.Mem) >= degradedPercent {
		status.Reasons = append(status.Reasons, "memory almost full")
	}
	if pctOf(status.Disk) >= degradedPercent {
		status.Reasons = append(status.Reasons, "disk almost full")
	}
	if sync.Failures >= syncDegradedAt {
		status.Reasons = append(status.Reasons, "sync failing: "+sync.LastError)
	}
	if ce != nil && ce.err != "" {
		status.Reasons = append(status.Reasons, "capabilities unavailable: "+ce.err)
	}
	if status.Stale {
		status.Reasons = append(status.Reasons, "metrics are from an earlier probe")
	}
	if len(status.Reasons) > 0 {
		status.Health = "degraded"
	} else {
		status.Health = "online"
	}
	return status
}

func itoa(n int) string { return strconv.Itoa(n) }
