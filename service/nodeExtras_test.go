package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/util/secretbox"

	"github.com/gin-gonic/gin"
)

// ---------- encrypted node secrets ----------

func TestNodeTokenIsSealedAtRestAndReadBackPlain(t *testing.T) {
	setupMasterForTest(t)
	if !secretbox.Ready() {
		t.Fatal("InitDB did not load a secret key")
	}
	f := newFakeNode(t, false)
	n := addFakeNode(t, "sealed", f)
	var raw string
	database.GetDB().Table("nodes").Select("token").Where("id = ?", n.Id).Scan(&raw)
	if !strings.HasPrefix(raw, secretbox.Prefix) || strings.Contains(raw, "tok-0") {
		t.Fatalf("token stored as %q, want sealed", raw)
	}
	var back model.Node
	database.GetDB().First(&back, n.Id)
	if back.Token != "tok-0" {
		t.Fatalf("token read back as %q", back.Token)
	}
	var s NodeRemoteService
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "hello"}); err != nil {
		t.Fatalf("hello with a sealed token: %v", err)
	}
	// Edit without a token keeps the stored one (opened, not double-sealed).
	edit, _ := json.Marshal(map[string]any{"id": n.Id, "name": "sealed", "baseUrl": f.srv.URL, "webPath": "/app/", "token": "", "enable": true, "allowPrivate": true})
	tx := database.GetDB().Begin()
	if err := (&NodeService{}).Save(tx, "edit", edit); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Commit()
	database.GetDB().First(&back, n.Id)
	if back.Token != "tok-0" {
		t.Fatalf("token after edit = %q", back.Token)
	}
}

func TestLegacyPlaintextTokensAreMigrated(t *testing.T) {
	setupMasterForTest(t)
	db := database.GetDB()
	db.Exec("INSERT INTO nodes (name, base_url, web_path, token, enable) VALUES ('old', 'https://n.example', '/app/', 'plain-tok', 1)")
	var n model.Node
	db.Where("name = ?", "old").First(&n)
	if n.Token != "plain-tok" {
		t.Fatalf("legacy plaintext read as %q", n.Token)
	}
	if err := database.MigrateSealSecrets(); err != nil {
		t.Fatal(err)
	}
	var raw string
	db.Table("nodes").Select("token").Where("name = ?", "old").Scan(&raw)
	if !secretbox.IsSealed(raw) {
		t.Fatalf("migration left %q", raw)
	}
	if count, err := database.UnsealNodeSecrets(); err != nil || count != 1 {
		t.Fatalf("unseal: %d %v", count, err)
	}
	db.Table("nodes").Select("token").Where("name = ?", "old").Scan(&raw)
	if raw != "plain-tok" {
		t.Fatalf("unsealed = %q", raw)
	}
}

// ---------- mTLS ----------

func selfSigned(t *testing.T, cn string, isCA bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: isCA, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	signer, signKey := tpl, key
	if parent != nil {
		signer, signKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	kb, _ := x509.MarshalECPrivateKey(key)
	return cert, key,
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func pinOf(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

func TestMutualTLSBetweenMasterAndNode(t *testing.T) {
	setupMasterForTest(t)
	caCert, caKey, caPEM, _ := selfSigned(t, "cluster-ca", true, nil, nil)
	_, _, clientPEM, clientKeyPEM := selfSigned(t, "master", false, caCert, caKey)
	pool, err := ParseClientCAs(caPEM)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	g := engine.Group("/app/apiv2")
	g.Use(RequireClientCert())
	g.GET("/:a", func(c *gin.Context) {
		c.JSON(200, gin.H{"success": true, "obj": gin.H{"apiVersion": 2}})
	})
	srv := httptest.NewUnstartedServer(engine)
	srv.TLS = &tls.Config{ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven}
	srv.StartTLS()
	defer srv.Close()

	node := model.Node{Id: 900, Name: "m", BaseUrl: srv.URL, WebPath: "/app/", Token: "t", AllowPrivate: true}
	node.CertPin = pinOf(srv.Certificate())
	if _, err := (&NodeService{}).nodeGet(&node, buildNodeHTTPClient(&node), "nodeHello", nil); err == nil {
		t.Fatal("node answered a master without a client certificate")
	}
	node.ClientCert, node.ClientKey = clientPEM, model.SecretString(clientKeyPEM)
	if err := validateNodeClientCert(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := (&NodeService{}).nodeGet(&node, buildNodeHTTPClient(&node), "nodeHello", nil); err != nil {
		t.Fatalf("with the client certificate: %v", err)
	}
	_, _, _, otherKey := selfSigned(t, "other", false, caCert, caKey)
	bad := node
	bad.ClientKey = model.SecretString(otherKey)
	if err := validateNodeClientCert(&bad); err == nil {
		t.Fatal("mismatched certificate/key accepted")
	}
	if _, err := ParseClientCAs("not a pem"); err == nil {
		t.Fatal("garbage CA accepted")
	}
}

// ---------- persistent breaker ----------

func TestBreakerStateSurvivesRestart(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "flaky", f)
	for i := 0; i < breakerThreshold+1; i++ {
		recordProbeOutcome(n.Id, NodeStatus{State: "offline"}, NodeStatus{}, false)
	}
	var row model.Node
	database.GetDB().First(&row, n.Id)
	if row.BreakerFails != breakerThreshold+1 || row.BreakerUntil == 0 {
		t.Fatalf("breaker not persisted: fails=%d until=%d", row.BreakerFails, row.BreakerUntil)
	}
	// "Restart": forget the in-memory state, load it back.
	breakerMu.Lock()
	nodeFails, nodeNextProbe, breakerPersisted = map[uint]int{}, map[uint]time.Time{}, map[uint]bool{}
	breakerMu.Unlock()
	breakerLoadOnce = sync.Once{}
	loadBreakerState()
	if open, _ := breakerOpen(n.Id); !open {
		t.Fatal("breaker not restored after restart")
	}
	if held, st := probeHeld(n.Id, time.Now()); !held || st.State != "offline" {
		t.Fatalf("restored node probed at full rate: held=%v state=%s", held, st.State)
	}
	recordProbeOutcome(n.Id, NodeStatus{State: "online"}, NodeStatus{}, false)
	database.GetDB().First(&row, n.Id)
	if row.BreakerFails != 0 || row.BreakerUntil != 0 {
		t.Fatalf("breaker row not cleared: %+v", row)
	}
}

// ---------- persistent operation queue ----------

func TestQueuedWriteRunsWhenNodeIsBack(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "later", f)
	var s NodeRemoteService
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "hello"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	data := json.RawMessage(`{"type":"vless","tag":"q-in","listen_port":8443,"users":[{"name":"secret-user","uuid":"11111111-1111-1111-1111-111111111111"}]}`)
	obj, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "save.inbounds", Action: "new", Data: data, Queue: true})
	if err != nil {
		t.Fatalf("queued write: %v", err)
	}
	var ans struct {
		Queued  bool   `json:"queued"`
		QueueId uint64 `json:"queueId"`
	}
	json.Unmarshal(obj, &ans)
	if !ans.Queued || ans.QueueId == 0 {
		t.Fatalf("answer %s, want queued", obj)
	}
	var row model.NodeQueuedOp
	database.GetDB().First(&row, ans.QueueId)
	if row.Status != "pending" || strings.Contains(row.Data, "secret-user") {
		t.Fatalf("queue row %+v (payload must be sealed)", row)
	}
	nodeStatusMu.Lock()
	nodeStatuses[n.Id] = NodeStatus{State: "offline"}
	nodeStatusMu.Unlock()
	DrainNodeQueue()
	database.GetDB().First(&row, ans.QueueId)
	if row.Status != "pending" {
		t.Fatalf("ran while offline: %+v", row)
	}
	f.mu.Lock()
	f.down = false
	f.mu.Unlock()
	nodeStatusMu.Lock()
	nodeStatuses[n.Id] = NodeStatus{State: "online"}
	nodeStatusMu.Unlock()
	DrainNodeQueue()
	database.GetDB().First(&row, ans.QueueId)
	if row.Status != "done" {
		t.Fatalf("queued write not run: %+v", row)
	}
	saves := f.callsTo("save")
	if len(saves) == 0 || saves[len(saves)-1].requestID != row.RequestId {
		t.Fatalf("replay must reuse the request id %s", row.RequestId)
	}
	if !strings.Contains(saves[len(saves)-1].form.Get("data"), "secret-user") {
		t.Fatal("payload not restored for the replay")
	}
	if err := s.CancelQueued(row.Id); err == nil {
		t.Fatal("cancelled a done operation")
	}
	rows, _ := s.ListQueue(n.Id, 10)
	if len(rows) != 1 {
		t.Fatalf("list = %d rows", len(rows))
	}
}

// ---------- faster pickup of direct node edits ----------

func TestDirectNodeEditRefreshesReplicasOnNextHeartbeat(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "direct", f)
	seedReplicaAndClient(t, n.Id, "old.example")
	var refreshed atomic.Int32
	done := make(chan struct{}, 4)
	prev := refreshOnRevisionChange
	refreshOnRevisionChange = func(*model.Node) { refreshed.Add(1); done <- struct{}{} }
	t.Cleanup(func() { refreshOnRevisionChange = prev })

	setRev := func(r string) { f.mu.Lock(); f.rev = r; f.mu.Unlock() }
	var ns NodeService
	setRev("aaaa")
	ns.RefreshAll() // first sighting: record only
	ns.RefreshAll() // unchanged
	if refreshed.Load() != 0 {
		t.Fatal("refreshed without a change")
	}
	setRev("bbbb") // someone edited an inbound on the node's own panel
	ns.RefreshAll()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("changed revision did not refresh the replicas")
	}
}

func TestInboundsRevisionFollowsChanges(t *testing.T) {
	setupMasterForTest(t)
	db := database.GetDB()
	db.Create(&model.Inbound{Type: "vless", Tag: "a", Options: json.RawMessage(`{"listen_port":1}`)})
	MarkLastUpdate(time.Now().Unix())
	r1 := InboundsRevision()
	if r1 == "" || InboundsRevision() != r1 {
		t.Fatal("revision not stable")
	}
	db.Model(model.Inbound{}).Where("tag = ?", "a").Update("options", json.RawMessage(`{"listen_port":2}`))
	MarkLastUpdate(time.Now().Unix() + 1)
	if InboundsRevision() == r1 {
		t.Fatal("revision did not change with the inbound")
	}
}

// ---------- panel update, tree, traffic ops ----------

func TestRemotePanelUpdateTreeAndStats(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	n := addFakeNode(t, "upd", f)
	var s NodeRemoteService
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "panel.update"}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("update without confirm: %v", err)
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "panel.update", Confirm: true,
		Params: map[string][]string{"version": {"v1.1.0"}}}); err != nil {
		t.Fatalf("confirmed update: %v", err)
	}
	if got := f.callsTo("nodeUpdate"); len(got) != 1 || got[0].form.Get("version") != "v1.1.0" {
		t.Fatalf("node update calls %+v", got)
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "panel.update", Confirm: true,
		Params: map[string][]string{"version": {"v9.9.9"}}}); err == nil {
		t.Fatal("update to a version the node does not see as latest succeeded")
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "panel.update", Confirm: true,
		Params: map[string][]string{"version": {"../x"}}}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("bad version: %v", err)
	}
	if lastOp(t, n.Id).Op != "panel.update" {
		t.Fatal("update not audited")
	}
	tree, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "nodes.tree"})
	if err != nil || !strings.Contains(string(tree), "edge-1") {
		t.Fatalf("tree %s %v", tree, err)
	}
	st, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "stats", Params: map[string][]string{"resource": {"user"}, "period": {"day"}}})
	if err != nil || !strings.Contains(string(st), `"user"`) {
		t.Fatalf("stats %s %v", st, err)
	}
	if _, err := s.Execute("admin", RemoteRequest{NodeId: n.Id, Op: "stats", Params: map[string][]string{"resource": {"secrets"}}}); !errors.Is(err, errRemoteRefused) {
		t.Fatalf("bad stats resource: %v", err)
	}
}

func TestNodeTreeInfoHasNoCredentials(t *testing.T) {
	setupMasterForTest(t)
	f := newFakeNode(t, false)
	addFakeNode(t, "child", f)
	rows, err := (&NodeService{}).NodeTreeInfo()
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "tok-0") || strings.Contains(string(b), "token") {
		t.Fatalf("tree leaks credentials: %s", b)
	}
}
