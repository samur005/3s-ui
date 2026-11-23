package service

import (
	"encoding/json"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/config"
	"github.com/samur005/3s-ui/core"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/qwdtt"
	"github.com/samur005/3s-ui/util/common"
	"github.com/samur005/3s-ui/xray"

	"github.com/shirou/gopsutil/v4/host"
)

var osStat = os.Stat

// The node side of multi-node management: what a 3S-UI install reports about
// itself when another panel manages it. Every answer here is read from the
// running binary or its database -- registered sing-box types, compiled build
// tags, supervised sidecars -- never from a hand-maintained list, so a build
// without a feature cannot claim it.

// NodeAPIVersion is the node-management API level this panel speaks, both as a
// node (what it answers) and as a master (what it may call).
//
//	1 -- legacy: status / onlines / inbounds / clients / save / logs /
//	     restartSb / maintenance (every 3S-UI before this API existed; such a
//	     node does not answer nodeHello at all).
//	2 -- nodeHello, nodeCapabilities, nodeHealth, nodeServices, nodePortCheck,
//	     nodeCheckConfig, nodeRotateToken / nodeRevokeToken, X-Request-Id
//	     idempotency on apiv2 POSTs.
const NodeAPIVersion = 2

// NodeMinMasterAPI is the lowest master API level a node of this version can be
// managed by. A master below it shows the node as incompatible instead of
// issuing calls whose shape changed.
const NodeMinMasterAPI = 1

// NodeHello is the cheap identity answer, also used to verify a rotated token.
type NodeHello struct {
	ApiVersion   int    `json:"apiVersion"`
	MinMasterApi int    `json:"minMasterApi"`
	AppVersion   string `json:"appVersion"`
	CoreVersion  string `json:"coreVersion"`
	Time         int64  `json:"time"`
}

// NodeTLSCaps reports TLS features by what is compiled in.
type NodeTLSCaps struct {
	TLS     bool `json:"tls"`
	UTLS    bool `json:"utls"`
	Reality bool `json:"reality"`
	ECH     bool `json:"ech"`
	ACME    bool `json:"acme"`
}

// NodeComponents reports the panel-supervised servers next to sing-box.
type NodeComponents struct {
	WireGuardServer bool `json:"wireguardServer"`
	AmneziaWG       bool `json:"amneziawg"`
	Qwdtt           bool `json:"qwdtt"`
	QwdttBinary     bool `json:"qwdttBinary"`
	Xray            bool `json:"xray"`
	XrayBinary      bool `json:"xrayBinary"`
}

// NodeCapabilities is the full self-description a master caches per node.
type NodeCapabilities struct {
	NodeHello
	OS            string         `json:"os"`
	Arch          string         `json:"arch"`
	GoVersion     string         `json:"goVersion"`
	BuildTags     []string       `json:"buildTags"`
	Inbounds      []string       `json:"inbounds"`
	Outbounds     []string       `json:"outbounds"`
	Endpoints     []string       `json:"endpoints"`
	Services      []string       `json:"services"`
	DNSServers    []string       `json:"dnsServers"`
	PanelInbounds []string       `json:"panelInbounds"`
	Transports    []string       `json:"transports"`
	TLS           NodeTLSCaps    `json:"tls"`
	Components    NodeComponents `json:"components"`
	Management    []string       `json:"management"`
	Features      []string       `json:"features"`
}

// NodeManagementOps is the allowlist of typed operations a master may run on
// this node through service/nodeRemote.go. A node answers it as-is; the master
// refuses anything outside it before a request leaves.
var NodeManagementOps = []string{
	"hello", "capabilities", "health", "services", "logs",
	"inbounds", "outbounds", "endpoints", "services.list", "clients", "config", "tls",
	"portCheck", "checkConfig", "checkOutbound",
	"save.inbounds", "save.outbounds", "save.endpoints", "save.services",
	"config.route", "config.dns",
	"core.restart", "core.stop", "core.start",
	"panel.restart",
	"token.rotate",
	"onlines", "stats",
	"panel.updateInfo", "panel.updateStatus", "panel.update",
	"nodes.tree",
}

var (
	buildTagsOnce sync.Once
	buildTagsList []string
)

// compiledBuildTags reads the -tags the binary was built with from its own
// build info. That is the ground truth for optional features such as uTLS /
// REALITY, QUIC or ACME: they are compiled in or stubbed out by those tags.
func compiledBuildTags() []string {
	buildTagsOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, s := range info.Settings {
			if s.Key == "-tags" {
				for _, t := range strings.Split(s.Value, ",") {
					if t = strings.TrimSpace(t); t != "" {
						buildTagsList = append(buildTagsList, t)
					}
				}
			}
		}
		sort.Strings(buildTagsList)
	})
	return buildTagsList
}

func hasString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// NodeHelloInfo answers apiv2 nodeHello.
func (s *ServerService) NodeHelloInfo() NodeHello {
	return NodeHello{
		ApiVersion:   NodeAPIVersion,
		MinMasterApi: NodeMinMasterAPI,
		AppVersion:   config.GetVersion(),
		CoreVersion:  singboxVersion,
		Time:         time.Now().Unix(),
	}
}

// NodeCapabilitiesInfo answers apiv2 nodeCapabilities.
func (s *ServerService) NodeCapabilitiesInfo() NodeCapabilities {
	tags := compiledBuildTags()
	inbounds := core.InboundRegistry().OptionTypes()
	outbounds := core.OutboundRegistry().OptionTypes()

	// v2ray transports: ws, http, httpupgrade and grpc (lite when with_grpc is
	// absent) are always compiled; QUIC needs with_quic.
	transports := []string{"grpc", "http", "httpupgrade", "ws"}
	if hasString(tags, "with_quic") {
		transports = append(transports, "quic")
	}

	panel := externalTypes()
	_, qwdttBinErr := statFile(qwdtt.BinaryPath())
	xst := xray.Get().Status(0)

	caps := NodeCapabilities{
		NodeHello:     s.NodeHelloInfo(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		GoVersion:     runtime.Version(),
		BuildTags:     tags,
		Inbounds:      inbounds,
		Outbounds:     outbounds,
		Endpoints:     core.EndpointRegistry().OptionTypes(),
		Services:      core.ServiceRegistry().OptionTypes(),
		DNSServers:    core.DNSTransportRegistry().OptionTypes(),
		PanelInbounds: panel,
		Transports:    transports,
		TLS: NodeTLSCaps{
			TLS: true,
			// sing-box's REALITY server and uTLS client both live behind with_utls.
			UTLS:    hasString(tags, "with_utls"),
			Reality: hasString(tags, "with_utls"),
			// ECH is part of the standard library TLS stack sing-box 1.14 uses.
			ECH:  true,
			ACME: hasString(tags, "with_acme"),
		},
		Components: NodeComponents{
			WireGuardServer: hasString(panel, "wg"),
			AmneziaWG:       hasString(panel, "awg"),
			Qwdtt:           hasString(panel, "qwdtt"),
			QwdttBinary:     qwdttBinErr == nil,
			Xray:            hasString(panel, xray.TypeVLESS),
			XrayBinary:      xst.BinaryOK,
		},
		Management: append([]string(nil), NodeManagementOps...),
		Features: []string{
			"cluster-sync", "bidirectional-sync", "request-id-idempotency",
			"token-rotation", "config-check", "port-check",
			"persistent-idempotency", "inbounds-revision", "panel-update", "node-tree",
			"mtls",
		},
	}
	return caps
}

// NodeHealthInfo answers apiv2 nodeHealth: one timestamped snapshot of system
// and panel state. Counters are totals since boot; the master turns two
// snapshots into rates.
func (s *ServerService) NodeHealthInfo() map[string]interface{} {
	now := time.Now().Unix()
	out := map[string]interface{}{
		"time": now,
		"cpu":  s.GetCpuPercent(),
		"mem":  s.GetMemInfo(),
		"swap": s.GetSwapInfo(),
		"disk": s.GetDiskInfo(),
		"net":  s.GetNetInfo(),
	}
	sb := s.GetSingboxInfo()
	out["core"] = sb
	if boot, err := hostBootTime(); err == nil && boot > 0 && uint64(now) > boot {
		out["uptime"] = uint64(now) - boot
	}
	out["connections"] = -1
	if tracker := liveConnTracker(); tracker != nil {
		out["connections"] = len(tracker.Sessions(nil))
	}
	db := database.GetDB()
	var inbounds, enabledInbounds, clients, clusterClients int64
	db.Model(model.Inbound{}).Where("node_id IS NULL").Count(&inbounds)
	db.Model(model.Inbound{}).Where("node_id IS NULL AND enable = ?", true).Count(&enabledInbounds)
	db.Model(model.Client{}).Count(&clients)
	db.Model(model.Client{}).Where("`group` = ?", clusterGroup).Count(&clusterClients)
	out["counts"] = map[string]int64{
		"inbounds": inbounds, "inboundsEnabled": enabledInbounds,
		"clients": clients, "clusterClients": clusterClients,
	}
	return out
}

// NodeServicesInfo answers apiv2 nodeServices: the state of every process the
// panel runs or supervises. Only components that exist in this build appear.
func (s *ServerService) NodeServicesInfo() map[string]interface{} {
	out := map[string]interface{}{
		"singbox": s.GetSingboxInfo(),
	}
	panel := externalTypes()
	if hasString(panel, xray.TypeVLESS) {
		st := (&XrayService{}).Status()
		st.Log = nil // logs go through the logs op, size-limited
		out["xray"] = st
	}
	if hasString(panel, "qwdtt") {
		if cfg, err := qwdtt.Load(); err == nil {
			st := qwdtt.Get().Status(cfg)
			// Paths and pids are host details a master does not need.
			out["qwdtt"] = map[string]interface{}{
				"supported": st.Supported, "enabled": st.Enabled, "running": st.Running,
				"uptime": st.Uptime, "binaryFound": st.BinaryFound, "routed": st.Routed,
				"error": st.Error, "lastExit": st.LastExit,
			}
		}
	}
	db := database.GetDB()
	for _, typ := range []string{"wg", "awg"} {
		if !hasString(panel, typ) {
			continue
		}
		var total, enabled int64
		db.Model(model.Inbound{}).Where("type = ? AND node_id IS NULL", typ).Count(&total)
		db.Model(model.Inbound{}).Where("type = ? AND node_id IS NULL AND enable = ?", typ, true).Count(&enabled)
		out[typ] = map[string]int64{"inbounds": total, "enabled": enabled}
	}
	return out
}

// PortCheckResult answers apiv2 nodePortCheck.
type PortCheckResult struct {
	Port      int    `json:"port"`
	Network   string `json:"network"`
	UsedBy    string `json:"usedBy,omitempty"` // tag of a panel inbound already on it
	Bindable  bool   `json:"bindable"`         // the OS let us bind it right now
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// CheckPort reports whether a port can take a new inbound: no panel inbound
// declares it (excluding the one being edited), and the OS lets us bind it. The
// bind is immediate and released at once; a port the running core holds fails
// it, which is the answer the operator needs.
func (s *ServerService) CheckPort(portStr, network, excludeTag string) (*PortCheckResult, error) {
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1 || port > 65535 {
		return nil, common.NewError("port must be 1-65535")
	}
	switch network {
	case "", "tcp":
		network = "tcp"
	case "udp":
	default:
		return nil, common.NewError("network must be tcp or udp")
	}
	res := &PortCheckResult{Port: port, Network: network}
	var inbounds []model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	for _, ib := range inbounds {
		if ib.Tag == excludeTag {
			continue
		}
		var opts struct {
			ListenPort int `json:"listen_port"`
		}
		if json.Unmarshal(ib.Options, &opts) == nil && opts.ListenPort == port {
			res.UsedBy = ib.Tag
			break
		}
	}
	addr := net.JoinHostPort("", strconv.Itoa(port))
	if network == "tcp" {
		if l, err := net.Listen("tcp", addr); err == nil {
			_ = l.Close()
			res.Bindable = true
		} else {
			res.Detail = err.Error()
		}
	} else {
		if c, err := net.ListenPacket("udp", addr); err == nil {
			_ = c.Close()
			res.Bindable = true
		} else {
			res.Detail = err.Error()
		}
	}
	res.Available = res.UsedBy == "" && res.Bindable
	return res, nil
}

// CheckBaseConfig validates a base config exactly as a save would, without
// saving it: the rule-set download rules and a full sing-box option check.
// An empty data validates the stored config.
func (s *ConfigService) CheckBaseConfig(data json.RawMessage) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		stored, err := s.SettingService.GetConfig()
		if err != nil {
			return err
		}
		data = json.RawMessage(stored)
	}
	if err := checkRuleSetDownloads(data); err != nil {
		return err
	}
	return s.validateBaseConfig(data)
}

func statFile(path string) (bool, error) {
	if path == "" {
		return false, common.NewError("no path")
	}
	_, err := osStat(path)
	return err == nil, err
}

func hostBootTime() (uint64, error) { return host.BootTime() }
