package service

import (
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/qwdtt"
)

// qWDTT across nodes.
//
// A node's qWDTT inbound is adopted like any other (a replica row on this
// panel) and clients are bound to the replica. The reconcile pushes them to
// the node in @cluster with their config, whose qwdtt block carries the
// client's own password; the node's supervisor writes it into its server's
// passwords.json (qwdtt/users.go), and this panel builds the node's link with
// the same password (QwdttService.nodeLinks). Traffic the node credits to its
// @cluster client comes back with the node traffic collection.

// QwdttEnsurePasswords fills in the per-client qWDTT password of every client
// bound to a qWDTT inbound or replica and, when one bound to a replica got
// one, flags the nodes that run qWDTT so the password reaches them on the
// next heartbeat instead of the hourly safety net.
func QwdttEnsurePasswords() {
	db := database.GetDB()
	n, err := qwdtt.EnsureClientPasswords(db)
	if err != nil {
		logger.Warning("qwdtt: filling client passwords: ", err)
		return
	}
	if n == 0 {
		return
	}
	bumpDirtyGen()
	if err := db.Model(model.Node{}).
		Where("enable = ? AND id IN (SELECT node_id FROM inbounds WHERE type = ? AND node_id IS NOT NULL)", true, qwdtt.InboundType).
		Update("dirty", true).Error; err != nil {
		logger.Warning("qwdtt: flagging nodes: ", err)
	}
}

// RefreshQwdttReplicas re-reads the qWDTT inbounds of every online node into
// their replicas. The VK hashes in a node's links change whenever a call ends,
// far more often than the hourly reconcile runs; this keeps the links the
// subscription builds from the replica within one run of the job of the
// node's current hashes. Only nodes with a qWDTT replica are asked.
func (s *NodeSyncService) RefreshQwdttReplicas() {
	var nodes []model.Node
	db := database.GetDB()
	if err := db.Model(model.Node{}).
		Where("enable = ? AND id IN (SELECT node_id FROM inbounds WHERE type = ? AND node_id IS NOT NULL)", true, qwdtt.InboundType).
		Find(&nodes).Error; err != nil {
		return
	}
	if len(nodes) == 0 {
		return
	}
	statuses := s.GetStatuses()
	for i := range nodes {
		node := &nodes[i]
		if st, ok := statuses[node.Id]; !ok || st.State != "online" {
			continue
		}
		func() {
			client := nodePushClient(node)
			defer closeIdle(client)
			tagToId, err := s.nodeInboundTagMap(node, client)
			if err != nil {
				logger.Warning("qwdtt: reading inbounds of node ", node.Name, ": ", err)
				return
			}
			if s.refreshReplicas(node, client, tagToId) {
				// The other protocols' stored links follow a moved replica too.
				s.refreshNodeLinks(node)
			}
		}()
	}
}
