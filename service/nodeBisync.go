package service

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/service/bisync"
	"github.com/samur005/3s-ui/util/common"
)

// runBidirectionalSync pulls the peer catalog, merges, stores conflicts, then
// pushes our enrolled catalog. Classic @cluster one_way push still runs after
// this for replica-bound clients that are not enrolled.
func (s *NodeSyncService) runBidirectionalSync(node *model.Node, client *http.Client) error {
	pid, err := bisync.PanelSyncID()
	if err != nil {
		return err
	}
	logger.Info("bisync: fetching catalog from ", node.Name)
	remote, err := s.fetchSyncCatalog(node, client)
	if err != nil {
		logger.Warning("bisync: fetch catalog failed: ", err)
		return err
	}
	logger.Info("bisync: got catalog proto=", remote.Proto, " inbounds=", len(remote.Inbounds), " clients=", len(remote.Clients))
	if remote.Proto < bisync.ProtocolVersion {
		return common.NewError("node does not support bidirectional sync (proto ", strconv.Itoa(remote.Proto), ")")
	}
	db := database.GetDB()

	mergeSide := func(kind string, remotes []bisync.Record) {
		for _, rem := range remotes {
			var localOK bool
			var local bisync.Record
			switch kind {
			case "client":
				var c model.Client
				if db.Where("sync_uid = ?", rem.UID).First(&c).Error == nil {
					localOK = true
					local = bisync.Record{
						UID: c.SyncUID, Kind: "client", Rev: c.SyncRev, Base: c.SyncBase,
						Origin: c.SyncOrigin, DeletedAt: c.DeletedAt,
					}
				}
			case "inbound":
				var ib model.Inbound
				if db.Where("sync_uid = ?", rem.UID).First(&ib).Error == nil {
					localOK = true
					local = bisync.Record{
						UID: ib.SyncUID, Kind: "inbound", Rev: ib.SyncRev, Base: ib.SyncBase,
						Origin: ib.SyncOrigin, DeletedAt: ib.DeletedAt,
					}
				}
			}
			res := bisync.MergeRemote(pid, localOK, local, rem)
			switch res.Action {
			case "echo", "noop":
				continue
			case "conflict":
				if err := bisync.StoreConflict(node.Id, res.Conflict); err != nil {
					logger.Warning("bisync conflict store: ", err)
				}
			case "apply", "tombstone":
				var aerr error
				if kind == "client" {
					aerr = bisync.ApplyClientRecord(res.Record)
				} else {
					aerr = bisync.ApplyInboundRecord(res.Record)
				}
				if aerr != nil {
					logger.Warning("bisync apply ", kind, " ", rem.UID, ": ", aerr)
				}
			}
		}
	}
	mergeSide("inbound", remote.Inbounds)
	mergeSide("client", remote.Clients)

	local, err := bisync.BuildCatalog()
	if err != nil {
		return err
	}
	return s.pushSyncCatalog(node, client, local)
}

func (s *NodeSyncService) fetchSyncCatalog(n *model.Node, client *http.Client) (*bisync.Catalog, error) {
	raw, err := s.nodeGet(n, client, "syncCatalog", nil)
	if err != nil {
		return nil, err
	}
	var cat bisync.Catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, err
	}
	return &cat, nil
}

func (s *NodeSyncService) pushSyncCatalog(n *model.Node, client *http.Client, cat *bisync.Catalog) error {
	body, err := json.Marshal(cat)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("data", string(body))
	_, err = s.nodePost(n, client, "syncCatalogApply", form)
	return err
}
