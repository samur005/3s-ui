package bisync

import "fmt"

// IngestRemoteCatalog merges every record from a peer push into local state.
// Used by apiv2 syncCatalogApply on the receiving panel.
func IngestRemoteCatalog(nodeID uint, remote *Catalog) error {
	if remote == nil {
		return fmt.Errorf("empty catalog")
	}
	pid, err := PanelSyncID()
	if err != nil {
		return err
	}
	applyList := func(kind string, list []Record) error {
		for _, rem := range list {
			localOK := false
			var local Record
			switch kind {
			case "client":
				if rec, ok := lookupClient(rem.UID); ok {
					localOK, local = true, rec
				}
			case "inbound":
				if rec, ok := lookupInbound(rem.UID); ok {
					localOK, local = true, rec
				}
			}
			res := MergeRemote(pid, localOK, local, rem)
			switch res.Action {
			case "echo", "noop":
				continue
			case "conflict":
				if err := StoreConflict(nodeID, res.Conflict); err != nil {
					return err
				}
			case "apply", "tombstone":
				var aerr error
				if kind == "client" {
					aerr = ApplyClientRecord(res.Record)
				} else {
					aerr = ApplyInboundRecord(res.Record)
				}
				if aerr != nil {
					return aerr
				}
			}
		}
		return nil
	}
	if err := applyList("inbound", remote.Inbounds); err != nil {
		return err
	}
	return applyList("client", remote.Clients)
}
