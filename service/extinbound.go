package service

import (
	"encoding/json"
	"sort"

	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/util/common"

	"gorm.io/gorm"
)

// External inbound types are rows on the Inbounds page that sing-box knows
// nothing about: qWDTT, WireGuard/AmneziaWG, and Xray-core VLESS. Each is
// driven by a process or device the panel supervises itself.
//
// For the panel they behave like any inbound -- a row with a tag, an enable
// switch, clients bound through clients.inbounds, a place in the list -- but
// they must never reach the sing-box config. Everything that builds core
// config or hot-plugs inbounds into the running core filters them out with
// externalTypes(); the save path hands them to their driver instead of the
// core. A new type is added by registering a driver, nothing else here changes.
type extDriver struct {
	// Type is the inbound type string stored in the row.
	Type string
	// Engine names what runs the inbound: EngineExternal for the panel's own
	// supervised servers (qWDTT, WireGuard, AmneziaWG). The Inbounds page groups
	// and labels rows by it; sing-box rows carry no driver and read as
	// EngineSingbox, and a second core would register under its own name.
	Engine string
	// Single refuses a second row of this type (one server per host).
	Single bool
	// Normalize validates the options a form sent and returns them in the
	// stored shape. stored holds the row's current options (nil for a new
	// row): what the form leaves out keeps its stored value. The row's id, tag
	// and enable switch are not part of them. id is the row's id (0 for a new
	// row) and tx the open transaction, for checks against the other rows.
	Normalize func(tx *gorm.DB, id uint, options, stored json.RawMessage) (json.RawMessage, error)
	// OnDelete runs inside the delete transaction, after the row is gone, to
	// remove what hangs off it (nil when nothing does).
	OnDelete func(tx *gorm.DB, id uint) error
	// Apply makes the stored state take effect (start, stop or restart the
	// process, adjust routing). It runs after the transaction committed, for
	// a save, a switch and a delete alike, and must read the DB itself.
	Apply func()
}

// Engines an inbound can run on.
const (
	EngineSingbox  = "singbox"
	EngineExternal = "external"
	EngineXray     = "xray"
)

var extDrivers = map[string]*extDriver{}

func registerExtDriver(d *extDriver) {
	if d.Engine == "" {
		d.Engine = EngineExternal
	}
	extDrivers[d.Type] = d
}

// engineOf says which engine runs an inbound type.
func engineOf(inboundType string) string {
	if d := extDriverFor(inboundType); d != nil {
		return d.Engine
	}
	return EngineSingbox
}

func extDriverFor(inboundType string) *extDriver { return extDrivers[inboundType] }

// externalTypes lists the registered types, for "type NOT IN ?" filters.
func externalTypes() []string {
	out := make([]string, 0, len(extDrivers))
	for t := range extDrivers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// saveExternal is InboundService.save for an inbound whose type has a driver:
// the row is validated and stored, clients are bound as for any inbound, and
// the driver is told once the transaction has committed. The core is not
// touched.
func (s *InboundService) saveExternal(tx *gorm.DB, act string, inbound *model.Inbound, drv *extDriver, initUserIds, hostname string, afterCommit *func()) error {
	if inbound.NodeId != nil {
		return common.NewError("inbound belongs to a node: manage it on the node panel")
	}
	var storedOptions json.RawMessage
	if act == "edit" {
		var old model.Inbound
		if err := tx.Model(model.Inbound{}).Select("id", "type", "enable", "options").Where("id = ?", inbound.Id).Find(&old).Error; err != nil {
			return err
		}
		if !inbound.EnableSet {
			inbound.Enable = old.Enable
		}
		storedOptions = old.Options
	}
	if err := tagFreeAcross(tx, model.Inbound{}, "inbound", inbound.Tag, inbound.Id); err != nil {
		return err
	}
	if drv.Single {
		var n int64
		if err := tx.Model(model.Inbound{}).
			Where("type = ? AND node_id IS NULL AND id <> ?", drv.Type, inbound.Id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return common.NewErrorf("only one %s inbound is supported: edit the existing one", drv.Type)
		}
	}
	opts, err := drv.Normalize(tx, inbound.Id, inbound.Options, storedOptions)
	if err != nil {
		return err
	}
	inbound.Options = opts
	inbound.TlsId = 0
	if len(inbound.Addrs) == 0 {
		inbound.Addrs = json.RawMessage("null")
	}
	if len(inbound.OutJson) == 0 {
		inbound.OutJson = json.RawMessage("null")
	}
	if err := tx.Omit("up", "down").Save(inbound).Error; err != nil {
		return err
	}
	if act == "new" {
		if err := s.ClientService.UpdateClientsOnInboundAdd(tx, initUserIds, inbound.Id, hostname); err != nil {
			return err
		}
	}
	*afterCommit = drv.Apply
	return nil
}
