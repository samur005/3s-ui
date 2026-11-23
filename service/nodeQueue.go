package service

import (
	"encoding/json"
	"net/url"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/common"
	"github.com/samur005/3s-ui/util/secretbox"
)

// Persistent queue for remote writes to nodes that are unreachable when the
// operator makes the change. Opt-in per request (RemoteRequest.Queue / the
// "queue" form field): a queued write is stored in node_queued_ops (payload
// sealed), survives a master restart, and is replayed by the heartbeat once
// the node is online again, with the original request id -- the node's
// persistent idempotency turns a write whose answer was lost into a replay.

const (
	queueMaxAttempts = 20
	queueBaseDelay   = 30 * time.Second
	queueMaxDelay    = 30 * time.Minute
	queueBatch       = 20
	queueKeepDone    = 7 * 24 * time.Hour
)

func (s *NodeRemoteService) enqueue(node *model.Node, actor string, req RemoteRequest, requestID, reason string) (json.RawMessage, error) {
	data, err := secretbox.Seal(string(req.Data))
	if err != nil {
		return nil, err
	}
	params := ""
	if len(req.Params) > 0 {
		m := map[string]string{}
		for k := range req.Params {
			m[k] = req.Params.Get(k)
		}
		b, _ := json.Marshal(m)
		if params, err = secretbox.Seal(string(b)); err != nil {
			return nil, err
		}
	}
	now := time.Now().Unix()
	row := model.NodeQueuedOp{
		NodeId: node.Id, Op: req.Op, Action: req.Action, Data: data, Params: params,
		Actor: actor, RequestId: requestID, Status: "pending",
		NextAt: now, LastError: truncate(reason, 500), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.GetDB().Create(&row).Error; err != nil {
		return nil, err
	}
	logger.Info("nodes: queued ", req.Op, " for ", node.Name, " (", reason, ")")
	out, _ := json.Marshal(map[string]any{"queued": true, "queueId": row.Id, "reason": reason})
	return out, nil
}

func queueDelay(attempts int) time.Duration {
	d := queueBaseDelay
	for i := 1; i < attempts && d < queueMaxDelay; i++ {
		d *= 2
	}
	if d > queueMaxDelay {
		d = queueMaxDelay
	}
	return d
}

var queueRunning sync.Mutex

// DrainNodeQueue runs due queued writes for nodes that are online now. Called
// by the heartbeat after the probes; single-flight.
func DrainNodeQueue() {
	db := database.GetDB()
	if db == nil || !queueRunning.TryLock() {
		return
	}
	defer queueRunning.Unlock()
	now := time.Now()
	var rows []model.NodeQueuedOp
	if err := db.Where("status = ? AND next_at <= ?", "pending", now.Unix()).Order("id").Limit(queueBatch).Find(&rows).Error; err != nil {
		return
	}
	statuses := (&NodeService{}).GetStatuses()
	blocked := map[uint]bool{} // keep per-node order: stop at a node's first failure
	svc := &NodeRemoteService{}
	for _, row := range rows {
		if blocked[row.NodeId] {
			continue
		}
		if st, ok := statuses[row.NodeId]; !ok || st.State == "offline" {
			blocked[row.NodeId] = true
			continue
		}
		req := RemoteRequest{NodeId: row.NodeId, Op: row.Op, Action: row.Action, Confirm: true, RequestId: row.RequestId}
		if plain, err := secretbox.Open(row.Data); err == nil && plain != "" {
			req.Data = json.RawMessage(plain)
		}
		if row.Params != "" {
			if plain, err := secretbox.Open(row.Params); err == nil {
				m := map[string]string{}
				if json.Unmarshal([]byte(plain), &m) == nil {
					req.Params = url.Values{}
					for k, v := range m {
						req.Params.Set(k, v)
					}
				}
			}
		}
		_, err := svc.Execute(row.Actor+" (queued)", req)
		upd := map[string]any{"attempts": row.Attempts + 1, "updated_at": time.Now().Unix()}
		switch {
		case err == nil:
			upd["status"] = "done"
			upd["last_error"] = ""
		case retryable(err) && row.Attempts+1 < queueMaxAttempts:
			upd["next_at"] = time.Now().Add(jitter(queueDelay(row.Attempts + 1))).Unix()
			upd["last_error"] = truncate(err.Error(), 500)
			blocked[row.NodeId] = true
		default:
			upd["status"] = "failed"
			upd["last_error"] = truncate(err.Error(), 500)
			blocked[row.NodeId] = true
		}
		db.Model(model.NodeQueuedOp{}).Where("id = ?", row.Id).Updates(upd)
	}
	db.Where("status IN ? AND updated_at < ?", []string{"done", "cancelled"}, now.Add(-queueKeepDone).Unix()).Delete(model.NodeQueuedOp{})
}

// ListQueue returns queued writes, newest first (payloads never included).
func (s *NodeRemoteService) ListQueue(nodeId uint, limit int) ([]model.NodeQueuedOp, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := database.GetDB().Model(model.NodeQueuedOp{}).Order("id desc").Limit(limit)
	if nodeId > 0 {
		q = q.Where("node_id = ?", nodeId)
	}
	var rows []model.NodeQueuedOp
	return rows, q.Find(&rows).Error
}

// CancelQueued cancels a pending write; RetryQueued makes a failed or pending
// one due now.
func (s *NodeRemoteService) CancelQueued(id uint64) error {
	res := database.GetDB().Model(model.NodeQueuedOp{}).Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]any{"status": "cancelled", "updated_at": time.Now().Unix()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return common.NewError("no pending queued operation with this id")
	}
	return nil
}

func (s *NodeRemoteService) RetryQueued(id uint64) error {
	res := database.GetDB().Model(model.NodeQueuedOp{}).Where("id = ? AND status IN ?", id, []string{"pending", "failed"}).
		Updates(map[string]any{"status": "pending", "next_at": time.Now().Unix(), "updated_at": time.Now().Unix()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return common.NewError("no pending or failed queued operation with this id")
	}
	return nil
}
