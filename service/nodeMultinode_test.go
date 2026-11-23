package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/op/go-logging"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
)

// ---------- unit: network policy ----------

func TestCheckNodeIPPolicy(t *testing.T) {
	cases := []struct {
		ip           string
		allowPrivate bool
		ok           bool
	}{
		{"8.8.8.8", false, true},
		{"2001:4860:4860::8888", false, true},
		{"169.254.169.254", true, false}, // cloud metadata: never
		{"fe80::1", true, false},
		{"0.0.0.0", true, false},
		{"224.0.0.1", true, false},
		{"::ffff:169.254.169.254", true, false}, // mapped form must not slip through
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, true},
		{"10.1.2.3", false, false},
		{"192.168.1.1", false, false},
		{"100.100.1.1", false, false}, // CGNAT
		{"100.100.1.1", true, true},
		{"fd00::1", false, false},
		{"::1", true, true},
	}
	for _, c := range cases {
		err := checkNodeIP(netip.MustParseAddr(c.ip), c.allowPrivate)
		if (err == nil) != c.ok {
			t.Errorf("checkNodeIP(%s, allowPrivate=%v) = %v, want ok=%v", c.ip, c.allowPrivate, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), errNodeAddressRefused.Error()) {
			t.Errorf("refusal for %s does not carry the policy marker: %v", c.ip, err)
		}
	}
}

func TestValidateNodeBaseURL(t *testing.T) {
	good := map[string]string{
		"https://node.example:2095":  "https://node.example:2095",
		" HTTPS://node.example/ ":    "https://node.example",
		"http://[2001:db8::1]:8080":  "http://[2001:db8::1]:8080",
		"https://node.example:2095/": "https://node.example:2095",
	}
	for in, want := range good {
		got, err := validateNodeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("validateNodeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "node.example", "ftp://node.example", "file:///etc/passwd",
		"https://user:pw@node.example", "https://node.example/app/",
		"https://node.example?x=1", "https://node.example#f", "https://:2095",
		"https://node.example:0", "https://node.example:70000", "gopher://x",
	}
	for _, in := range bad {
		if got, err := validateNodeBaseURL(in); err == nil {
			t.Errorf("validateNodeBaseURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestNodeCheckRedirect(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	first := mk("http://node.example:2095/app/apiv2/status")
	if err := nodeCheckRedirect(mk("https://node.example:2095/app/apiv2/status"), []*http.Request{first}); err != nil {
		t.Errorf("same-host upgrade refused: %v", err)
	}
	if err := nodeCheckRedirect(mk("https://evil.example/collect"), []*http.Request{first}); err == nil {
		t.Error("cross-host redirect followed: the Token header would leak")
	}
	tlsFirst := mk("https://node.example/app/")
	if err := nodeCheckRedirect(mk("http://node.example/app/"), []*http.Request{tlsFirst}); err == nil {
		t.Error("https -> http downgrade followed")
	}
	if err := nodeCheckRedirect(first, []*http.Request{first, first, first}); err == nil {
		t.Error("redirect loop not cut")
	}
}

// The guard sits in the dialer, so a loopback node is refused before any byte
// (and the token) is sent unless the node allows private addresses.
func TestNodeClientRefusesPrivateAddressUnlessAllowed(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Write([]byte(`{"success":true,"obj":{}}`))
	}))
	defer srv.Close()

	denied := buildNodeHTTPClient(&model.Node{BaseUrl: srv.URL})
	defer closeIdle(denied)
	if _, err := denied.Get(srv.URL); err == nil || !strings.Contains(err.Error(), errNodeAddressRefused.Error()) {
		t.Fatalf("loopback node without allowPrivate: err = %v, want a policy refusal", err)
	}
	mu.Lock()
	if hits != 0 {
		t.Fatalf("server was reached %d times despite the refusal", hits)
	}
	mu.Unlock()

	allowed := buildNodeHTTPClient(&model.Node{BaseUrl: srv.URL, AllowPrivate: true})
	defer closeIdle(allowed)
	resp, err := allowed.Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback node with allowPrivate: %v", err)
	}
	resp.Body.Close()
}

// ---------- unit: backoff, breaker, health ----------

func TestBreakerDelayAndSyncBackoff(t *testing.T) {
	if d := breakerDelay(breakerThreshold - 1); d != 0 {
		t.Errorf("below threshold: %v, want 0", d)
	}
	if d := breakerDelay(breakerThreshold); d != breakerBase {
		t.Errorf("at threshold: %v, want %v", d, breakerBase)
	}
	if d := breakerDelay(breakerThreshold + 1); d != 2*breakerBase {
		t.Errorf("threshold+1: %v, want %v", d, 2*breakerBase)
	}
	if d := breakerDelay(1000); d != breakerMax {
		t.Errorf("cap: %v, want %v", d, breakerMax)
	}
	if d := syncBackoffFor(0); d != reconcileBackoff {
		t.Errorf("sync backoff floor: %v, want %v", d, reconcileBackoff)
	}
	if d := syncBackoffFor(2); d != 4*reconcileBackoff {
		t.Errorf("sync backoff after 2: %v", d)
	}
	if d := syncBackoffFor(100); d != syncBackoffMax {
		t.Errorf("sync backoff cap: %v, want %v", d, syncBackoffMax)
	}
	for i := 0; i < 200; i++ {
		j := jitter(10 * time.Second)
		if j < 8*time.Second || j > 12*time.Second {
			t.Fatalf("jitter out of ±20%%: %v", j)
		}
	}
}

func TestDecorateHealthPrecedence(t *testing.T) {
	online := NodeStatus{State: "online", Latency: 10}
	v2 := &capsEntry{caps: &NodeCapabilities{NodeHello: NodeHello{ApiVersion: 2, MinMasterApi: 1}}}

	cases := []struct {
		name    string
		status  NodeStatus
		enabled bool
		ce      *capsEntry
		sync    NodeSyncSt
		want    string
	}{
		{"disabled wins", NodeStatus{State: "offline"}, false, nil, NodeSyncSt{}, "disabled"},
		{"offline", NodeStatus{State: "offline", Error: "dial"}, true, v2, NodeSyncSt{Running: true}, "offline"},
		{"incompatible", online, true, &capsEntry{caps: &NodeCapabilities{NodeHello: NodeHello{ApiVersion: 9, MinMasterApi: NodeAPIVersion + 1}}}, NodeSyncSt{}, "incompatible"},
		{"syncing", online, true, v2, NodeSyncSt{Running: true}, "syncing"},
		{"core stopped is degraded", NodeStatus{State: "core-stopped"}, true, v2, NodeSyncSt{}, "degraded"},
		{"high latency", NodeStatus{State: "online", Latency: degradedLatency + 1}, true, v2, NodeSyncSt{}, "degraded"},
		{"memory full", NodeStatus{State: "online", Mem: NodeMem{Current: 99, Total: 100}}, true, v2, NodeSyncSt{}, "degraded"},
		{"sync failing", online, true, v2, NodeSyncSt{Failures: syncDegradedAt, LastError: "x"}, "degraded"},
		{"stale", NodeStatus{State: "online", Stale: true}, true, v2, NodeSyncSt{}, "degraded"},
		{"legacy node is fine", online, true, &capsEntry{legacy: true}, NodeSyncSt{}, "online"},
		{"healthy", online, true, v2, NodeSyncSt{}, "online"},
	}
	for _, c := range cases {
		got := decorateHealthWith(c.status, c.enabled, c.ce, c.sync)
		if got.Health != c.want {
			t.Errorf("%s: health = %q (reasons %v), want %q", c.name, got.Health, got.Reasons, c.want)
		}
		if got.Health == "degraded" && len(got.Reasons) == 0 {
			t.Errorf("%s: degraded without a reason", c.name)
		}
	}
	if got := decorateHealthWith(online, true, &capsEntry{legacy: true}, NodeSyncSt{}); got.ApiVersion != 1 {
		t.Errorf("legacy node apiVersion = %d, want 1", got.ApiVersion)
	}
}

// ---------- unit: subscription policy ----------

func TestFilterNodeLinks(t *testing.T) {
	links := json.RawMessage(`[
		{"type":"local","remark":"main","uri":"vless://a"},
		{"type":"external","remark":"[tokyo] vless","uri":"vless://t"},
		{"type":"external","remark":"[paris] vless","uri":"vless://p"},
		{"type":"external","remark":"[paris] dup","uri":"vless://p"},
		{"type":"external","remark":"manual","uri":"trojan://m"}
	]`)
	got := filterNodeLinks(links, []string{nodeLinkPrefix("tokyo")})
	var out []map[string]string
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	var remarks []string
	for _, l := range out {
		remarks = append(remarks, l["remark"])
	}
	if strings.Join(remarks, "|") != "main|[paris] vless|manual" {
		t.Errorf("filtered remarks = %v", remarks)
	}
	if string(filterNodeLinks(json.RawMessage(`not json`), nil)) != "not json" {
		t.Error("malformed links must pass through unchanged")
	}
	same := json.RawMessage(`[{"type":"external","remark":"[a] x","uri":"u"}]`)
	if string(filterNodeLinks(same, nil)) != string(same) {
		t.Error("untouched links must be returned byte-identical")
	}
	// A node named "a" must not exclude a node named "ab".
	near := filterNodeLinks(json.RawMessage(`[{"type":"external","remark":"[ab] x","uri":"u"}]`), []string{nodeLinkPrefix("a")})
	if !strings.Contains(string(near), "[ab] x") {
		t.Error("prefix match leaked across node names")
	}
}

// ---------- unit: capabilities come from the binary ----------

func TestNodeCapabilitiesInfoReportsRegistries(t *testing.T) {
	caps := (&ServerService{}).NodeCapabilitiesInfo()
	if caps.ApiVersion != NodeAPIVersion || caps.MinMasterApi != NodeMinMasterAPI {
		t.Fatalf("api levels = %d/%d", caps.ApiVersion, caps.MinMasterApi)
	}
	for _, want := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		if !hasString(caps.Inbounds, want) {
			t.Errorf("inbound %q missing from %v", want, caps.Inbounds)
		}
	}
	for _, want := range []string{"direct", "block", "selector"} {
		if !hasString(caps.Outbounds, want) && want != "block" {
			t.Errorf("outbound %q missing from %v", want, caps.Outbounds)
		}
	}
	if len(caps.DNSServers) == 0 {
		t.Error("no DNS server types reported")
	}
	for _, op := range caps.Management {
		if _, ok := remoteOps[op]; !ok && op != "token.rotate" {
			t.Errorf("node advertises %q but the master has no such op", op)
		}
	}
	for op := range remoteOps {
		if !hasString(caps.Management, op) {
			t.Errorf("master op %q is not advertised by this node build", op)
		}
	}
}

// ---------- integration: master + fake nodes ----------

// fakeNode is a 3S-UI node seen from the master: the apiv2 actions the
// master calls, with a v2 or legacy (API level 1) personality.
type fakeNode struct {
	t       *testing.T
	srv     *httptest.Server
	legacy  bool
	token   string
	mu      sync.Mutex
	calls   []fakeCall
	config  string
	minted  int
	revoked []string
	badNew  bool // nodeHello rejects freshly minted tokens
	// outServer, when set, is the address the node reports for inbound 7 in
	// the full (by-id) inbounds shape the replica refresh reads.
	outServer string
	// down makes every request fail at the transport level (node offline).
	down bool
	// rev is the sys.inboundsRev the status answer carries.
	rev string
}

type fakeCall struct {
	action, requestID, token string
	form                     url.Values
	query                    url.Values
}

func newFakeNode(t *testing.T, legacy bool) *fakeNode {
	f := &fakeNode{t: t, legacy: legacy, token: "tok-0",
		config: `{"log":{"level":"info"},"route":{"rules":[],"final":"direct"},"dns":{"servers":[]}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNode) ok(w http.ResponseWriter, obj string) {
	w.Write([]byte(`{"success":true,"msg":"","obj":` + obj + `}`))
}

func (f *fakeNode) fail(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(map[string]any{"success": false, "msg": msg, "obj": nil})
	w.Write(b)
}

func (f *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	action := strings.TrimPrefix(r.URL.Path, "/app/apiv2/")
	f.mu.Lock()
	if f.down {
		f.mu.Unlock()
		panic(http.ErrAbortHandler)
	}
	f.calls = append(f.calls, fakeCall{action: action, requestID: r.Header.Get("X-Request-Id"),
		token: r.Header.Get("Token"), form: r.PostForm, query: r.URL.Query()})
	tokenOK := r.Header.Get("Token") == f.token || (!f.badNew && f.minted > 0 && r.Header.Get("Token") == "tok-new")
	f.mu.Unlock()
	if !tokenOK {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	v2only := map[string]bool{"nodeHello": true, "nodeCapabilities": true, "nodeHealth": true,
		"nodeServices": true, "nodePortCheck": true, "nodeCheckConfig": true,
		"nodeRotateToken": true, "nodeRevokeToken": true,
		"nodeUpdateInfo": true, "nodeUpdate": true, "nodeUpdateStatus": true, "nodeTree": true}
	if f.legacy && v2only[action] {
		f.fail(w, "unknown action: "+action)
		return
	}
	switch action {
	case "status":
		f.mu.Lock()
		rev := f.rev
		f.mu.Unlock()
		f.ok(w, `{"sbd":{"running":true},"cpu":1,"sys":{"inboundsRev":"`+rev+`"}}`)
	case "nodeUpdateInfo":
		f.ok(w, `{"current":"v1.0.0","latest":"v1.1.0","updateAvailable":true,"canSelfUpdate":true}`)
	case "nodeUpdate":
		if v := r.PostForm.Get("version"); v != "" && v != "v1.1.0" {
			f.fail(w, "latest release is v1.1.0, not "+v)
			return
		}
		f.ok(w, `null`)
	case "nodeUpdateStatus":
		f.ok(w, `{"state":"idle"}`)
	case "nodeTree":
		f.ok(w, `[{"id":1,"name":"edge-1","host":"10.0.0.5","state":"online"}]`)
	case "stats":
		f.ok(w, `[{"dateTime":1700000000,"resource":"`+r.URL.Query().Get("resource")+`","tag":"x","direction":true,"traffic":10}]`)
	case "onlines":
		f.ok(w, `{"user":[]}`)
	case "nodeHello":
		f.ok(w, `{"apiVersion":2,"minMasterApi":1,"appVersion":"test"}`)
	case "nodeCapabilities":
		caps := NodeCapabilities{
			NodeHello: NodeHello{ApiVersion: 2, MinMasterApi: 1, AppVersion: "test"},
			Inbounds:  []string{"vless", "trojan"}, Outbounds: []string{"direct", "selector"},
			Management: append([]string(nil), NodeManagementOps...),
		}
		b, _ := json.Marshal(caps)
		f.ok(w, string(b))
	case "nodeHealth":
		f.ok(w, `{"cpu":3}`)
	case "inbounds":
		f.mu.Lock()
		srv := f.outServer
		f.mu.Unlock()
		if r.URL.Query().Get("id") != "" && srv != "" {
			f.ok(w, `{"inbounds":[{"id":7,"tag":"rep","type":"vless","enable":true,"listen_port":443,`+
				`"out_json":{"type":"vless","tag":"rep","server":"`+srv+`","server_port":443}}]}`)
			return
		}
		f.ok(w, `{"inbounds":[{"id":7,"tag":"rep","type":"vless"}]}`)
	case "config":
		f.mu.Lock()
		cfg := f.config
		f.mu.Unlock()
		f.ok(w, `{"config":`+cfg+`}`)
	case "nodeCheckConfig":
		if !json.Valid([]byte(r.PostForm.Get("data"))) {
			f.fail(w, "invalid config")
			return
		}
		f.ok(w, `null`)
	case "save":
		if r.PostForm.Get("object") == "config" {
			f.mu.Lock()
			f.config = r.PostForm.Get("data")
			f.mu.Unlock()
		}
		f.ok(w, `{}`)
	case "restartSb", "maintenance", "restartApp":
		f.ok(w, `null`)
	case "nodeRotateToken":
		f.mu.Lock()
		f.minted++
		f.mu.Unlock()
		f.ok(w, `{"token":"tok-new","id":42,"previousId":41}`)
	case "nodeRevokeToken":
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("id"))
		f.mu.Unlock()
		f.ok(w, `null`)
	default:
		f.fail(w, "unknown action: "+action)
	}
}

func (f *fakeNode) callsTo(action string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.action == action {
			out = append(out, c)
		}
	}
	return out
}

func resetNodeGlobalsForTest(t *testing.T) {
	reset := func() {
		capsMu.Lock()
		nodeCapsM = map[uint]*capsEntry{}
		capsMu.Unlock()
		breakerMu.Lock()
		nodeFails = map[uint]int{}
		nodeNextProbe = map[uint]time.Time{}
		breakerPersisted = map[uint]bool{}
		breakerMu.Unlock()
		nodeRevMu.Lock()
		nodeRevs = map[uint]string{}
		nodeRevRunning = map[uint]bool{}
		nodeRevMu.Unlock()
		rateMu.Lock()
		nodeBuckets = map[uint]*tokenBucket{}
		rateMu.Unlock()
		syncStMu.Lock()
		nodeSyncStat = map[uint]NodeSyncSt{}
		syncStMu.Unlock()
		nodeStatusMu.Lock()
		nodeStatuses = map[uint]NodeStatus{}
		nodeStatusMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func setupMasterForTest(t *testing.T) (dirty *[]uint) {
	t.Helper()
	logger.InitLogger(logging.CRITICAL)
	if err := database.InitDB(filepath.Join(t.TempDir(), "master.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	resetNodeGlobalsForTest(t)
	var mu sync.Mutex
	marked := []uint{}
	prev := afterRemoteChange
	afterRemoteChange = func(id uint) {
		mu.Lock()
		marked = append(marked, id)
		mu.Unlock()
	}
	t.Cleanup(func() { afterRemoteChange = prev })
	return &marked
}

func addFakeNode(t *testing.T, name string, f *fakeNode) model.Node {
	t.Helper()
	n := model.Node{Name: name, BaseUrl: f.srv.URL, WebPath: "/app/", Token: model.SecretString(f.token), Enable: true, AllowPrivate: true}
	if err := database.GetDB().Create(&n).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	return n
}

func lastOp(t *testing.T, nodeId uint) model.NodeOperation {
	t.Helper()
	var op model.NodeOperation
	if err := database.GetDB().Where("node_id = ?", nodeId).Order("id desc").First(&op).Error; err != nil {
		t.Fatalf("no audit row for node %d: %v", nodeId, err)
	}
	return op
}

func TestRemoteManagementAcrossNodes(t *testing.T) {
	dirty := setupMasterForTest(t)
	fa, fb, fl := newFakeNode(t, false), newFakeNode(t, false), newFakeNode(t, true)
	a := addFakeNode(t, "alpha", fa)
	b := addFakeNode(t, "beta", fb)
	l := addFakeNode(t, "legacy", fl)
	var s NodeRemoteService

	// Reads work on every node, legacy included, each with a request id.
	for _, n := range []model.Node{a, b, l} {
		obj, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "inbounds"})
		if err != nil || !strings.Contains(string(obj), `"rep"`) {
			t.Fatalf("inbounds on %s: %s, %v", n.Name, obj, err)
		}
	}
	if c := fa.callsTo("inbounds"); len(c) != 1 || len(c[0].requestID) != 32 {
		t.Fatalf("inbounds call on alpha = %+v, want one with a request id", c)
	}

	// Typed save: an inbound type the node does not run is refused here.
	_, err := s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "save.inbounds", Action: "new",
		Data: json.RawMessage(`{"type":"hysteria2","tag":"h2"}`)})
	if !errors.Is(err, errRemoteRefused) {
		t.Fatalf("unsupported type: err = %v, want a refusal", err)
	}
	if op := lastOp(t, a.Id); op.Status != "refused" || op.Op != "save.inbounds" {
		t.Errorf("audit after refusal = %+v", op)
	}
	if len(fa.callsTo("save")) != 0 {
		t.Fatal("refused save reached the node")
	}

	// A supported one goes through, is audited, and marks the node dirty.
	_, err = s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "save.inbounds", Action: "new",
		Data: json.RawMessage(`{"type":"vless","tag":"v-new"}`)})
	if err != nil {
		t.Fatalf("save vless: %v", err)
	}
	saves := fa.callsTo("save")
	if len(saves) != 1 || saves[0].form.Get("object") != "inbounds" || saves[0].form.Get("action") != "new" {
		t.Fatalf("save on node = %+v", saves)
	}
	op := lastOp(t, a.Id)
	if op.Status != "ok" || op.RequestId != saves[0].requestID || op.Actor != "admin" {
		t.Errorf("audit row = %+v, want ok with the node call's request id", op)
	}
	if len(*dirty) != 1 || (*dirty)[0] != a.Id {
		t.Errorf("dirty marks = %v, want [%d]", *dirty, a.Id)
	}
	var stored model.Node
	database.GetDB().First(&stored, a.Id)
	if !stored.Dirty {
		t.Error("node not marked dirty in the DB")
	}

	// An adopted inbound may not be deleted from under the replica.
	if err := database.GetDB().Create(&model.Inbound{Type: "vless", Tag: "rep", NodeId: &b.Id}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute("admin", RemoteRequest{NodeId: b.Id, Op: "save.inbounds", Action: "del", Data: json.RawMessage(`"rep"`)})
	if !errors.Is(err, errRemoteRefused) {
		t.Fatalf("adopted delete: err = %v, want a refusal", err)
	}
	// ...nor renamed.
	_, err = s.Execute("admin", RemoteRequest{NodeId: b.Id, Op: "save.inbounds", Action: "edit",
		Data: json.RawMessage(`{"id":7,"type":"vless","tag":"renamed"}`)})
	if !errors.Is(err, errRemoteRefused) {
		t.Fatalf("adopted rename: err = %v, want a refusal", err)
	}

	// Legacy node: v2-only ops refused before a request, legacy ops allowed.
	before := len(fl.callsTo("nodeCheckConfig"))
	if _, err := s.Execute("admin", RemoteRequest{NodeId: l.Id, Op: "checkConfig", Data: json.RawMessage(`{}`)}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("checkConfig on legacy: %v, want a refusal", err)
	}
	if len(fl.callsTo("nodeCheckConfig")) != before {
		t.Error("refused op reached the legacy node")
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: l.Id, Op: "core.restart"}); err != nil {
		t.Fatalf("core.restart on legacy: %v", err)
	}
	if h, err := s.Execute("admin", RemoteRequest{NodeId: l.Id, Op: "health"}); err != nil || !strings.Contains(string(h), `"legacy":true`) {
		t.Fatalf("legacy health = %s, %v", h, err)
	}

	// Dangerous ops need confirm.
	if _, err := s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "panel.restart"}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("panel.restart without confirm: %v", err)
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "panel.restart", Confirm: true}); err != nil {
		t.Fatalf("panel.restart confirmed: %v", err)
	}
	// Unknown ops never reach anything.
	if _, err := s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "shell"}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("unknown op: %v", err)
	}

	// Route section: validated on the node, then saved with the rest kept.
	_, err = s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "config.route",
		Data: json.RawMessage(`{"rules":[{"outbound":"direct"}],"final":"block"}`)})
	if err != nil {
		t.Fatalf("config.route: %v", err)
	}
	if len(fa.callsTo("nodeCheckConfig")) != 1 {
		t.Error("config was not checked before saving")
	}
	fa.mu.Lock()
	cfg := fa.config
	fa.mu.Unlock()
	if !strings.Contains(cfg, `"final":"block"`) || !strings.Contains(cfg, `"log":{"level":"info"}`) {
		t.Errorf("saved config = %s, want the new route and the old log section", cfg)
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: a.Id, Op: "config.dns", Data: json.RawMessage(`[1]`)}); !errors.Is(err, errRemoteRefused) {
		t.Errorf("non-object dns section: %v, want a refusal", err)
	}

	// Disabled node: refused, nothing sent.
	database.GetDB().Model(&model.Node{}).Where("id = ?", b.Id).Update("enable", false)
	n := len(fb.callsTo("inbounds"))
	if _, err := s.Execute("admin", RemoteRequest{NodeId: b.Id, Op: "inbounds"}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("disabled node: %v", err)
	}
	if len(fb.callsTo("inbounds")) != n {
		t.Error("disabled node was called")
	}

	// Audit is listed newest first with a total.
	rows, total, err := s.ListOperations(a.Id, 2, 0)
	if err != nil || len(rows) != 2 || total < 4 || rows[0].Id < rows[1].Id {
		t.Errorf("ListOperations = %d rows, total %d, %v", len(rows), total, err)
	}
}

func TestRemoteOfflineNodeFailsAndIsAudited(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "gone", f)
	var s NodeRemoteService
	// Learn capabilities while it is up, then take it down.
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "hello"}); err != nil {
		t.Fatal(err)
	}
	f.srv.Close()
	_, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "core.restart"})
	if err == nil {
		t.Fatal("restart on an offline node succeeded")
	}
	op := lastOp(t, n.Id)
	if op.Status != "failed" || op.Attempts != remoteMaxRetries+1 {
		t.Errorf("audit = %+v, want failed after %d attempts", op, remoteMaxRetries+1)
	}
	// With the breaker open the master refuses without dialing.
	breakerMu.Lock()
	nodeFails[n.Id] = breakerThreshold
	nodeNextProbe[n.Id] = time.Now().Add(time.Minute)
	breakerMu.Unlock()
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "inbounds"}); !errors.Is(err, errRemoteRefused) {
		t.Errorf("open breaker: %v, want a refusal", err)
	}
}

func TestRemoteBulkAndSubscriptionExclusion(t *testing.T) {
	setupMasterForTest(t)
	f1, f2, f3 := newFakeNode(t, false), newFakeNode(t, false), newFakeNode(t, false)
	n1, n2, n3 := addFakeNode(t, "one", f1), addFakeNode(t, "two", f2), addFakeNode(t, "three", f3)
	var s NodeRemoteService

	res, err := s.Bulk("admin", []uint{n1.Id, n3.Id}, "subExclude", "")
	if err != nil || len(res) != 2 {
		t.Fatalf("bulk subExclude = %v, %v", res, err)
	}
	links := json.RawMessage(`[
		{"type":"external","remark":"[one] a","uri":"vless://1"},
		{"type":"external","remark":"[two] a","uri":"vless://2"},
		{"type":"external","remark":"[three] a","uri":"vless://3"}]`)
	got := string(FilterNodeLinks(links))
	if strings.Contains(got, "[one]") || strings.Contains(got, "[three]") || !strings.Contains(got, "[two]") {
		t.Errorf("subscription after excluding one and three = %s", got)
	}
	if _, err := s.Bulk("admin", []uint{n1.Id}, "subInclude", ""); err != nil {
		t.Fatal(err)
	}
	if got := string(FilterNodeLinks(links)); !strings.Contains(got, "[one]") {
		t.Errorf("re-included node still hidden: %s", got)
	}

	if _, err := s.Bulk("admin", []uint{n1.Id, n2.Id}, "setGroup", "eu"); err != nil {
		t.Fatal(err)
	}
	var groups []string
	database.GetDB().Model(model.Node{}).Order("id").Pluck("node_group", &groups)
	if strings.Join(groups, ",") != "eu,eu," {
		t.Errorf("groups = %v", groups)
	}
	if _, err := s.Bulk("admin", []uint{n2.Id}, "disable", ""); err != nil {
		t.Fatal(err)
	}
	var enabled []bool
	database.GetDB().Model(model.Node{}).Order("id").Pluck("enable", &enabled)
	if enabled[1] || !enabled[0] {
		t.Errorf("enable flags = %v", enabled)
	}
	var changes int64
	database.GetDB().Model(model.Changes{}).Where("key = ? AND action LIKE ?", "nodes", "bulk:%").Count(&changes)
	if changes != 4 {
		t.Errorf("bulk changes rows = %d, want 4", changes)
	}
	if _, err := s.Bulk("admin", []uint{n1.Id}, "drop", ""); !errors.Is(err, errRemoteRefused) {
		t.Errorf("unknown bulk op: %v", err)
	}
	if _, err := s.Bulk("admin", nil, "enable", ""); !errors.Is(err, errRemoteRefused) {
		t.Errorf("empty selection: %v", err)
	}
}

func TestRemoteTokenRotation(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "rot", f)
	var s NodeRemoteService
	if err := s.RotateToken("admin", n.Id); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	var stored model.Node
	database.GetDB().First(&stored, n.Id)
	if stored.Token != "tok-new" {
		t.Errorf("stored token = %q, want the new one", stored.Token)
	}
	if strings.Join(f.revoked, ",") != "41" {
		t.Errorf("revoked = %v, want the previous token id 41", f.revoked)
	}
	if rv := f.callsTo("nodeRevokeToken"); len(rv) != 1 || rv[0].token != "tok-new" {
		t.Errorf("revoke must authenticate with the new token: %+v", rv)
	}
	if op := lastOp(t, n.Id); op.Op != "token.rotate" || op.Status != "ok" {
		t.Errorf("audit = %+v", op)
	}
	if strings.Contains(lastOp(t, n.Id).Error, "tok-") {
		t.Error("token leaked into the audit trail")
	}
}

func TestRemoteTokenRotationRollsBack(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	f.badNew = true
	n := addFakeNode(t, "rot", f)
	var s NodeRemoteService
	if err := s.RotateToken("admin", n.Id); err == nil {
		t.Fatal("rotation with an unverifiable token succeeded")
	}
	var stored model.Node
	database.GetDB().First(&stored, n.Id)
	if stored.Token != "tok-0" {
		t.Errorf("stored token = %q, want the old one kept", stored.Token)
	}
	if strings.Join(f.revoked, ",") != "42" {
		t.Errorf("revoked = %v, want the minted token 42 undone", f.revoked)
	}
	if op := lastOp(t, n.Id); op.Status != "failed" {
		t.Errorf("audit = %+v", op)
	}

	// Legacy nodes cannot rotate: refused with guidance, nothing changed.
	fl := newFakeNode(t, true)
	l := addFakeNode(t, "old", fl)
	if err := s.RotateToken("admin", l.Id); !errors.Is(err, errRemoteRefused) {
		t.Errorf("legacy rotate: %v, want a refusal", err)
	}
}

func TestRemoteRateLimit(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "busy", f)
	var s NodeRemoteService
	var refusedCount int
	for i := 0; i < int(remoteRateBurst)+5; i++ {
		if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "hello"}); errors.Is(err, errRemoteRefused) {
			refusedCount++
		}
	}
	if refusedCount == 0 {
		t.Error("burst above the limit was never refused")
	}
}

// ---------- node side: token rotation primitives ----------

func TestRotateAndRevokeCallerToken(t *testing.T) {
	setupMasterForTest(t)
	db := database.GetDB()
	other := model.Tokens{Token: "other-admin", UserId: 99}
	caller := model.Tokens{Token: "caller-token", Desc: "master", Expiry: 12345, UserId: 1}
	db.Create(&other)
	db.Create(&caller)
	var us UserService

	tok, newId, prevId, err := us.RotateCallerToken("caller-token")
	if err != nil || tok == "" || tok == "caller-token" || prevId != caller.Id || newId == caller.Id {
		t.Fatalf("rotate = %q %d %d %v", tok, newId, prevId, err)
	}
	var fresh model.Tokens
	db.First(&fresh, newId)
	if fresh.UserId != 1 || fresh.Expiry != 12345 || !strings.HasPrefix(fresh.Desc, "master (rotated ") {
		t.Errorf("fresh token row = %+v", fresh)
	}
	if _, _, _, err := us.RotateCallerToken("nope"); err == nil {
		t.Error("unknown caller rotated a token")
	}
	if err := us.RevokeTokenAsCaller(tok, newId); err == nil {
		t.Error("caller revoked its own token mid-call")
	}
	if err := us.RevokeTokenAsCaller(tok, other.Id); err == nil {
		t.Error("caller revoked another admin's token")
	}
	if err := us.RevokeTokenAsCaller(tok, caller.Id); err != nil {
		t.Fatalf("revoke old: %v", err)
	}
	var n int64
	db.Model(model.Tokens{}).Where("token = ?", "caller-token").Count(&n)
	if n != 0 {
		t.Error("old token still stored")
	}
}

// ---------- subscription links follow node updates without a reconcile ----------

func seedReplicaAndClient(t *testing.T, nodeId uint, server string) (model.Inbound, model.Client) {
	t.Helper()
	db := database.GetDB()
	rep := model.Inbound{Type: "vless", Tag: "rep", NodeId: &nodeId, Enable: true,
		Options: json.RawMessage(`{"listen_port":443}`),
		OutJson: json.RawMessage(`{"type":"vless","tag":"rep","server":"` + server + `","server_port":443}`)}
	if err := db.Create(&rep).Error; err != nil {
		t.Fatal(err)
	}
	cl := model.Client{Name: "alice", Remark: "alice", Enable: true,
		Config:   json.RawMessage(`{"vless":{"uuid":"11111111-2222-3333-4444-555555555555"}}`),
		Inbounds: json.RawMessage(`[` + strconv.FormatUint(uint64(rep.Id), 10) + `]`)}
	if err := db.Create(&cl).Error; err != nil {
		t.Fatal(err)
	}
	return rep, cl
}

func clientLinks(t *testing.T, id uint) string {
	t.Helper()
	var c model.Client
	if err := database.GetDB().First(&c, id).Error; err != nil {
		t.Fatal(err)
	}
	return string(c.Links)
}

// A host change made through the master (remote inbound edit) reaches the
// stored subscription links before Execute returns -- no reconcile involved
// (afterRemoteChange is stubbed by setupMasterForTest).
func TestNodeLinksFollowRemoteAddressChangeWithoutReconcile(t *testing.T) {
	dirty := setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "alpha", f)
	_, cl := seedReplicaAndClient(t, n.Id, "old.example")

	RefreshNodeLinksFromReplicas(n.Id)
	if l := clientLinks(t, cl.Id); !strings.Contains(l, "old.example:443") || !strings.Contains(l, "[alpha] rep") {
		t.Fatalf("initial links = %s", l)
	}

	f.mu.Lock()
	f.outServer = "new.example"
	f.mu.Unlock()
	var s NodeRemoteService
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "save.inbounds", Action: "edit",
		Data: json.RawMessage(`{"id":7,"type":"vless","tag":"rep","listen_port":443}`)}); err != nil {
		t.Fatalf("remote edit: %v", err)
	}
	l := clientLinks(t, cl.Id)
	if !strings.Contains(l, "new.example:443") || strings.Contains(l, "old.example") {
		t.Fatalf("links right after the edit = %s, want the new host only", l)
	}
	var rep model.Inbound
	database.GetDB().Where("node_id = ? AND tag = ?", n.Id, "rep").First(&rep)
	if !strings.Contains(string(rep.OutJson), "new.example") {
		t.Errorf("replica row not refreshed: %s", rep.OutJson)
	}
	if len(*dirty) != 1 {
		t.Errorf("client reconcile should still be scheduled once, got %v", *dirty)
	}
}

// Editing the node on the master (here: a rename plus a new panel URL, with
// the node unreachable) regenerates its links right after the commit, from
// the stored replica rows. Driven the way ConfigService.Save drives it (own tx,
// SaveWithLinks, hook after commit) without starting a core.
func TestNodeEditRegeneratesLinksImmediatelyEvenOffline(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "alpha", f)
	_, cl := seedReplicaAndClient(t, n.Id, "node.example")
	RefreshNodeLinksFromReplicas(n.Id)
	f.srv.Close() // offline from here on

	// Drop the link set to prove the edit rebuilds it rather than relying on
	// the in-place prefix rewrite alone.
	database.GetDB().Model(&model.Client{}).Where("id = ?", cl.Id).Update("links", json.RawMessage(`[]`))

	edit, _ := json.Marshal(map[string]any{"id": n.Id, "name": "beta", "baseUrl": "https://moved.example:2095",
		"webPath": "/app/", "token": "", "enable": true, "allowPrivate": true, "syncMode": "one_way"})
	// The node branch of ConfigService.Save: its own tx, the hook after commit.
	save := func(data []byte) error {
		tx := database.GetDB().Begin()
		after, err := (&NodeService{}).SaveWithLinks(tx, "edit", data)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit().Error; err != nil {
			return err
		}
		if after != nil {
			after()
		}
		return nil
	}
	if err := save(edit); err != nil {
		t.Fatalf("edit node: %v", err)
	}
	l := clientLinks(t, cl.Id)
	if !strings.Contains(l, "[beta] rep") || strings.Contains(l, "[alpha]") || !strings.Contains(l, "node.example:443") {
		t.Fatalf("links right after the edit = %s", l)
	}

	// Rename path with links present: the prefix follows, nothing duplicated.
	edit, _ = json.Marshal(map[string]any{"id": n.Id, "name": "gamma", "baseUrl": "https://moved.example:2095",
		"webPath": "/app/", "token": "", "enable": true, "allowPrivate": true, "syncMode": "one_way"})
	if err := save(edit); err != nil {
		t.Fatalf("rename node: %v", err)
	}
	l = clientLinks(t, cl.Id)
	if strings.Count(l, "[gamma] rep") != 1 || strings.Contains(l, "[beta]") {
		t.Fatalf("links after rename = %s", l)
	}
}
