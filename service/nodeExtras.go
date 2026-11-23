package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sync"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/common"
)

// ---------- node side: inbounds revision (faster pickup of direct edits) ----------
//
// InboundsRevision is a short hash over everything a master's replica and the
// links built from it depend on (tag, type, listen options, TLS, address
// book, out_json). It rides the status answer the master's 5 s heartbeat
// already fetches (sys.inboundsRev), so an edit made directly in the node's
// own panel reaches the master's replicas and subscription links within one
// heartbeat instead of at the hourly reconcile. Recomputed only when the
// panel's change counter moved.

var (
	revMu   sync.Mutex
	revSeq  int64 = -1
	revHash string
)

func InboundsRevision() string {
	seq := lastUpdateSeq.Load()
	revMu.Lock()
	defer revMu.Unlock()
	if seq == revSeq && revHash != "" {
		return revHash
	}
	db := database.GetDB()
	if db == nil {
		return ""
	}
	var rows []model.Inbound
	if err := db.Model(model.Inbound{}).Where("node_id = 0 OR node_id IS NULL").Order("id").Find(&rows).Error; err != nil {
		return revHash
	}
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r.Tag))
		h.Write([]byte{0})
		h.Write([]byte(r.Type))
		h.Write([]byte{0})
		h.Write(r.Options)
		h.Write([]byte{0})
		h.Write(r.Addrs)
		h.Write([]byte{0})
		h.Write(r.OutJson)
		h.Write([]byte{0})
		h.Write([]byte(uintStr(r.TlsId)))
		h.Write([]byte{1})
	}
	revSeq = seq
	revHash = hex.EncodeToString(h.Sum(nil))[:16]
	return revHash
}

func uintStr(v uint) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// ---------- master side: react to a changed revision ----------

var (
	nodeRevMu      sync.Mutex
	nodeRevs       = map[uint]string{}
	nodeRevRunning = map[uint]bool{}
)

// noteInboundsRevision records a node's revision from the heartbeat and, when
// it moved since the last one seen, refreshes that node's replicas and links
// in the background (single-flight per node). The first sighting only
// records: on master start the hourly reconcile / dirty sync covers it.
func noteInboundsRevision(n *model.Node, rev string) {
	if rev == "" || n == nil {
		return
	}
	nodeRevMu.Lock()
	prev, seen := nodeRevs[n.Id]
	nodeRevs[n.Id] = rev
	if !seen || prev == rev || nodeRevRunning[n.Id] {
		nodeRevMu.Unlock()
		return
	}
	nodeRevRunning[n.Id] = true
	nodeRevMu.Unlock()
	node := *n
	go func() {
		defer func() {
			nodeRevMu.Lock()
			delete(nodeRevRunning, node.Id)
			nodeRevMu.Unlock()
		}()
		var replicas int64
		if db := database.GetDB(); db != nil {
			db.Model(model.Inbound{}).Where("node_id = ?", node.Id).Count(&replicas)
		}
		if replicas == 0 {
			return
		}
		logger.Info("nodes: ", node.Name, " changed its inbounds; refreshing replicas and links")
		refreshOnRevisionChange(&node)
	}()
}

// refreshOnRevisionChange is swapped by tests.
var refreshOnRevisionChange = func(n *model.Node) { (&NodeSyncService{}).RefreshReplicasAndLinks(n) }

func forgetInboundsRevision(id uint) {
	nodeRevMu.Lock()
	delete(nodeRevs, id)
	nodeRevMu.Unlock()
}

// ---------- node side: panel self-update requested by a master ----------

// StartUpdateExpect starts the regular, checksum-verified self-update
// (UpdateService.run: SHA256SUMS of the release, smoke test, atomic swap). A
// non-empty version must equal the latest release the node sees, so a master
// that approved "v1.2.3" never installs a newer release published meanwhile.
func (s *UpdateService) StartUpdateExpect(version string) error {
	if version != "" {
		latest, err := s.LatestRelease()
		if err != nil {
			return common.NewErrorf("cannot read the latest release: %v", err)
		}
		if normalizeVer(latest) != normalizeVer(version) {
			return common.NewErrorf("latest release is %s, not %s -- refresh and confirm again", latest, version)
		}
	}
	return s.StartUpdate()
}

// UpdateInfoForMaster is apiv2 nodeUpdateInfo: what the node runs, what it
// could update to, and whether it can update itself at all.
func (s *UpdateService) UpdateInfoForMaster(currentVersion string) map[string]any {
	ok, reason := s.CanSelfUpdate()
	out := map[string]any{"current": currentVersion, "canSelfUpdate": ok, "reason": reason, "docker": s.InDocker()}
	if latest, err := s.LatestRelease(); err == nil {
		out["latest"] = latest
		out["updateAvailable"] = shouldInstall(latest, currentVersion)
	} else {
		out["latestError"] = err.Error()
	}
	return out
}

// ---------- node side: node tree ----------

// NodeTreeInfo is apiv2 nodeTree: this panel's own nodes (it may be a master
// for another tier), without credentials -- name, host, labels and live state.
func (s *NodeService) NodeTreeInfo() ([]map[string]any, error) {
	var nodes []model.Node
	if err := database.GetDB().Model(model.Node{}).Select("id", "name", "enable", "base_url", "node_group", "location").Find(&nodes).Error; err != nil {
		return nil, err
	}
	st := s.GetStatuses()
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		host := ""
		if u, err := url.Parse(n.BaseUrl); err == nil {
			host = u.Hostname()
		}
		row := map[string]any{"id": n.Id, "name": n.Name, "enable": n.Enable, "host": host, "group": n.NodeGroup, "location": n.Location, "state": "disabled"}
		if status, ok := st[n.Id]; ok {
			row["state"] = status.State
			row["health"] = status.Health
			row["appVersion"] = status.AppVersion
			row["checkedAt"] = status.CheckedAt
		}
		out = append(out, row)
	}
	return out, nil
}
