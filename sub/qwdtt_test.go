package sub

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/qwdtt"
)

// qwdttFixture sets up two clients and a qWDTT inbound. The clients named in
// bound ride the inbound (all of them when none is named): riding is how a
// client gets the link now.
func qwdttFixture(t *testing.T, mut func(*qwdtt.Config), bound ...string) {
	t.Helper()
	t.Setenv(qwdtt.EnvVkHash, "")
	dir := t.TempDir()
	t.Setenv("S3UI_DB_FOLDER", dir)
	if err := database.InitDB(filepath.Join(dir, "sub.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
	for _, name := range []string{"alice", "bob"} {
		if err := database.GetDB().Create(&model.Client{
			Name: name, Enable: true,
			Config: json.RawMessage(`{"qwdtt":{"name":"` + name + `","password":"pw-` + name + `"}}`),
			Links:  json.RawMessage(`[{"type":"external","remark":"r","uri":"vless://x@h:443"}]`),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	c := qwdtt.Default()
	c.Enabled = true
	c.Password = "secret"
	c.SubHost = "1.2.3.4:56000"
	c.VkHashes = "h1,h2"
	c.Remark = "Home"
	if mut != nil {
		mut(&c)
	}
	if _, err := qwdtt.Save(c); err != nil {
		t.Fatal(err)
	}
	if len(bound) == 0 {
		bound = []string{"alice", "bob"}
	}
	ids, _ := json.Marshal([]uint{qwdtt.InboundID()})
	if err := database.GetDB().Model(model.Client{}).Where("name IN ?", bound).Update("inbounds", json.RawMessage(ids)).Error; err != nil {
		t.Fatal(err)
	}
}

// decodeSub undoes the base64 the subscription is wrapped in by default.
func decodeSub(body string) string {
	if raw, err := base64.StdEncoding.DecodeString(body); err == nil {
		return string(raw)
	}
	return body
}

// qwdttLines returns the subscription lines that are qwdtt links.
func qwdttLines(t *testing.T, subID string) []string {
	t.Helper()
	s := SubService{}
	res, _, err := s.GetSubs(subID)
	if err != nil {
		t.Fatalf("GetSubs(%s): %v", subID, err)
	}
	var out []string
	for _, l := range strings.Split(decodeSub(*res), "\n") {
		if strings.HasPrefix(l, "qwdtt://") {
			out = append(out, l)
		}
	}
	return out
}

// The qWDTT import contract: exactly one qwdtt://config? line, never a
// trailing wdtt:// (the client parses the whole clipboard as one URI, so a
// second line corrupts the password and the DTLS handshake dies).
func TestSubscriptionCarriesOneQwdttLink(t *testing.T) {
	qwdttFixture(t, nil)
	s := SubService{}
	res, _, err := s.GetSubs("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decodeSub(*res), "vless://x@h:443") {
		t.Error("the client's own links were dropped")
	}
	lines := qwdttLines(t, "alice")
	if len(lines) != 1 {
		t.Fatalf("want one qwdtt line, got %d: %q", len(lines), lines)
	}
	got := lines[0]
	if !strings.HasPrefix(got, "qwdtt://config?") {
		t.Fatalf("link = %q", got)
	}
	for _, want := range []string{"name=Home", "peer=1.2.3.4%3A56000", "pass=pw-alice", "hashes=h1%2Ch2", "port=9000", "workers=16"} {
		if !strings.Contains(got, want) {
			t.Errorf("link lacks %q: %s", want, got)
		}
	}
}

func TestSubscriptionOmitsQwdttWhenOffOrNotGranted(t *testing.T) {
	qwdttFixture(t, func(c *qwdtt.Config) { c.Enabled = false })
	if l := qwdttLines(t, "alice"); len(l) != 0 {
		t.Errorf("disabled qWDTT still handed out a link: %q", l)
	}

	qwdttFixture(t, nil, "alice")
	if l := qwdttLines(t, "alice"); len(l) != 1 {
		t.Errorf("selected client got %d links", len(l))
	}
	if l := qwdttLines(t, "bob"); len(l) != 0 {
		t.Errorf("client without access got a link: %q", l)
	}
}

func TestSubscriptionFollowsHashChanges(t *testing.T) {
	qwdttFixture(t, nil)
	before := qwdttLines(t, "alice")[0]
	c, _ := qwdtt.Load()
	c.VkHashes = "fresh"
	if _, err := qwdtt.Save(c); err != nil {
		t.Fatal(err)
	}
	after := qwdttLines(t, "alice")[0]
	if before == after || !strings.Contains(after, "hashes=fresh") {
		t.Errorf("subscription kept the old hash:\n%s\n%s", before, after)
	}
}

func TestSubscriptionLinkNeedsAPeer(t *testing.T) {
	// No SubHost, and a host with no panel/sub domain: the link falls back to
	// the detected outbound address, or is omitted when there is none -- never
	// emitted with an empty peer.
	qwdttFixture(t, func(c *qwdtt.Config) { c.SubHost = "" })
	for _, l := range qwdttLines(t, "alice") {
		if strings.Contains(l, "peer=&") || strings.Contains(l, "peer=%3A") {
			t.Errorf("empty peer in %s", l)
		}
	}
}

func TestDashboardInfoListsQwdttLink(t *testing.T) {
	qwdttFixture(t, nil)
	s := SubService{}
	info, err := s.getClientInfoJSON("alice")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, l := range info.Links {
		if strings.HasPrefix(l, "qwdtt://config?") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("dashboard links = %v", info.Links)
	}
}

// Shared mode, the shape of every inbound stored before per-client
// passwords: everyone gets the inbound's one password.
func TestSubscriptionSharedModeHandsOutTheSharedPassword(t *testing.T) {
	qwdttFixture(t, func(c *qwdtt.Config) { c.UserMode = qwdtt.UserModeShared })
	for _, name := range []string{"alice", "bob"} {
		l := qwdttLines(t, name)
		if len(l) != 1 || !strings.Contains(l[0], "pass=secret") {
			t.Errorf("%s: %q", name, l)
		}
	}
}

// Per-client mode never falls back to the owner's password: a client with no
// password of its own yet gets no link until the supervisor gives it one.
func TestSubscriptionPerClientNeverLeaksTheOwnerPassword(t *testing.T) {
	qwdttFixture(t, nil)
	if err := database.GetDB().Model(model.Client{}).Where("name = ?", "bob").
		Update("config", json.RawMessage(`{}`)).Error; err != nil {
		t.Fatal(err)
	}
	if l := qwdttLines(t, "bob"); len(l) != 0 {
		t.Errorf("client without a password got %q", l)
	}
	for _, l := range qwdttLines(t, "alice") {
		if strings.Contains(l, "pass=secret") {
			t.Errorf("owner password handed out: %s", l)
		}
	}
}

// ?format=qwdtt is the qWDTT app's import document; its User-Agent gets it on
// the bare URL.
func TestQwdttSubscriptionDocument(t *testing.T) {
	qwdttFixture(t, nil)
	s := SubService{}
	res, _, err := s.getQwdttSub("alice")
	if err != nil {
		t.Fatal(err)
	}
	var doc qwdtt.Subscription
	if err := json.Unmarshal([]byte(*res), &doc); err != nil {
		t.Fatalf("%v: %s", err, *res)
	}
	if doc.SubscriptionName != "alice" || len(doc.Profiles) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	p := doc.Profiles[0]
	if p.Peer != "1.2.3.4:56000" || p.Password != "pw-alice" || p.Hashes != "h1,h2" || p.Port != 9000 || p.Workers != 16 {
		t.Errorf("profile = %+v", p)
	}
	if got := formatForUserAgent("qWDTT-Subscription/1.0"); got != "qwdtt" {
		t.Errorf("qWDTT app User-Agent -> %q", got)
	}
}

// The plain list can be told to leave qwdtt:// out (a strict proxy app); the
// qWDTT document still has it.
func TestQwdttSubLinksSetting(t *testing.T) {
	qwdttFixture(t, nil)
	if err := database.GetDB().Create(&model.Setting{Key: "qwdttSubLinks", Value: "false"}).Error; err != nil {
		t.Fatal(err)
	}
	if l := qwdttLines(t, "alice"); len(l) != 0 {
		t.Errorf("qwdtt links in the plain list with the setting off: %q", l)
	}
	s := SubService{}
	if _, _, err := s.getQwdttSub("alice"); err != nil {
		t.Errorf("qWDTT document: %v", err)
	}
}

// A node's qWDTT inbound reaches the master's subscription through its
// replica: the node's peer and hashes, the client's own password, the node's
// name in front, and nothing for a node left out of subscriptions.
func TestQwdttNodeReplicaLinks(t *testing.T) {
	qwdttFixture(t, nil)
	db := database.GetDB()
	node := &model.Node{Name: "fi", BaseUrl: "https://fi.example.com:2095", WebPath: "/app/", Token: "t"}
	if err := db.Create(node).Error; err != nil {
		t.Fatal(err)
	}
	nodeID := node.Id
	rep := &model.Inbound{Type: qwdtt.InboundType, Tag: "qwdtt-fi", NodeId: &nodeID, Enable: true, EnableSet: true,
		Addrs: json.RawMessage("null"), OutJson: json.RawMessage("null"),
		Options: json.RawMessage(`{"listenAddr":"0.0.0.0:57000","password":"nodeowner","userMode":"perClient","remark":"Fin"}`)}
	if err := db.Create(rep).Error; err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal([]uint{qwdtt.InboundID(), rep.Id})
	if err := db.Model(model.Client{}).Where("name = ?", "alice").Update("inbounds", json.RawMessage(ids)).Error; err != nil {
		t.Fatal(err)
	}
	lines := qwdttLines(t, "alice")
	if len(lines) != 2 {
		t.Fatalf("want master + node link, got %q", lines)
	}
	nodeLink := lines[1]
	for _, want := range []string{"peer=fi.example.com%3A57000", "pass=pw-alice", "name=%5Bfi%5D+Fin", "hashes=h1%2Ch2"} {
		if !strings.Contains(nodeLink, want) {
			t.Errorf("node link lacks %q: %s", want, nodeLink)
		}
	}
	if strings.Contains(nodeLink, "nodeowner") {
		t.Errorf("node owner password handed out: %s", nodeLink)
	}
	s := SubService{}
	res, _, err := s.getQwdttSub("alice")
	if err != nil {
		t.Fatal(err)
	}
	var doc qwdtt.Subscription
	_ = json.Unmarshal([]byte(*res), &doc)
	if len(doc.Profiles) != 2 {
		t.Errorf("document profiles = %+v", doc.Profiles)
	}

	if err := db.Model(model.Node{}).Where("id = ?", nodeID).Update("sub_exclude", true).Error; err != nil {
		t.Fatal(err)
	}
	if l := qwdttLines(t, "alice"); len(l) != 1 {
		t.Errorf("excluded node still in the subscription: %q", l)
	}
}
