package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/samur005/3s-ui/core"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// targetNode is a node panel with a real (in-memory) inbound table, enough to
// follow create, edit and delete from the master's inbound form end to end.
type targetNode struct {
	mu       sync.Mutex
	srv      *httptest.Server
	nextId   uint
	inbounds map[uint]map[string]any // node id -> panel shape
	saves    []string                // action:requestId of every save
	seen     map[string]bool         // X-Request-Id dedup, like api/idempotency.go
	busy     map[int]string          // port -> tag of a non-panel listener
}

func newTargetNode(t *testing.T) *targetNode {
	f := &targetNode{nextId: 1, inbounds: map[uint]map[string]any{}, seen: map[string]bool{}, busy: map[int]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *targetNode) ok(w http.ResponseWriter, obj string) {
	_, _ = w.Write([]byte(`{"success":true,"obj":` + obj + `}`))
}

func (f *targetNode) fail(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(msg)
	_, _ = w.Write([]byte(`{"success":false,"msg":` + string(b) + `}`))
}

func (f *targetNode) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = r.ParseForm()
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.mu.Lock()
	defer f.mu.Unlock()
	switch action {
	case "nodeCapabilities":
		caps := NodeCapabilities{
			NodeHello:  NodeHello{ApiVersion: 2, MinMasterApi: 1, AppVersion: "test"},
			Inbounds:   []string{"vless", "hysteria2"},
			Management: append([]string(nil), NodeManagementOps...),
		}
		b, _ := json.Marshal(caps)
		f.ok(w, string(b))
	case "tls":
		f.ok(w, `{"tls":[{"id":5,"name":"node-cert"}]}`)
	case "nodePortCheck":
		port, _ := strconv.Atoi(r.URL.Query().Get("port"))
		res := PortCheckResult{Port: port, Network: r.URL.Query().Get("network"), Bindable: true}
		for _, ib := range f.inbounds {
			if p, _ := ib["listen_port"].(float64); int(p) == port && ib["tag"] != r.URL.Query().Get("exclude") {
				res.UsedBy = ib["tag"].(string)
			}
		}
		if who, ok := f.busy[port]; ok {
			res.Bindable = false
			res.Detail = "address already in use (" + who + ")"
		}
		res.Available = res.Bindable && res.UsedBy == ""
		b, _ := json.Marshal(res)
		f.ok(w, string(b))
	case "inbounds":
		var rows []string
		ids := r.URL.Query().Get("id")
		for id, ib := range f.inbounds {
			if ids != "" && !strings.Contains(","+ids+",", ","+strconv.Itoa(int(id))+",") {
				continue
			}
			row := map[string]any{}
			for k, v := range ib {
				row[k] = v
			}
			row["id"] = id
			if ids == "" {
				row = map[string]any{"id": id, "type": ib["type"], "tag": ib["tag"], "listen_port": ib["listen_port"], "users": []string{}}
			} else {
				row["out_json"] = map[string]any{"type": ib["type"], "tag": ib["tag"], "server": "node.example", "server_port": ib["listen_port"]}
				row["addrs"] = []any{}
			}
			b, _ := json.Marshal(row)
			rows = append(rows, string(b))
		}
		f.ok(w, `{"inbounds":[`+strings.Join(rows, ",")+`]}`)
	case "save":
		rid := r.Header.Get("X-Request-Id")
		f.saves = append(f.saves, r.PostForm.Get("action")+":"+rid)
		if f.seen[rid] {
			f.ok(w, `{}`) // replay
			return
		}
		f.seen[rid] = true
		data := r.PostForm.Get("data")
		switch r.PostForm.Get("action") {
		case "new":
			var m map[string]any
			_ = json.Unmarshal([]byte(data), &m)
			if _, ok := m["node_id"]; ok {
				f.fail(w, "node_id must not reach the node")
				return
			}
			f.inbounds[f.nextId] = m
			f.nextId++
		case "edit":
			var m map[string]any
			_ = json.Unmarshal([]byte(data), &m)
			id := uint(m["id"].(float64))
			if _, ok := f.inbounds[id]; !ok {
				f.fail(w, "no such inbound")
				return
			}
			delete(m, "id")
			f.inbounds[id] = m
		case "del":
			var tag string
			_ = json.Unmarshal([]byte(data), &tag)
			for id, ib := range f.inbounds {
				if ib["tag"] == tag {
					delete(f.inbounds, id)
				}
			}
		}
		f.ok(w, `{}`)
	default:
		f.fail(w, "unknown action: "+action)
	}
}

func (f *targetNode) byTag(tag string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ib := range f.inbounds {
		if ib["tag"] == tag {
			return ib
		}
	}
	return nil
}

func TestNodeInboundCreateEditDelete(t *testing.T) {
	setupMasterForTest(t)
	f := newTargetNode(t)
	node := model.Node{Name: "fra", BaseUrl: f.srv.URL, WebPath: "/app/", Token: "tok", Enable: true, AllowPrivate: true}
	db := database.GetDB()
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"vless": {"name": "alice", "uuid": "8b5e6d3c-3f3e-4c1a-9b1e-2b8a4f6f0a11"}}`)
	alice := model.Client{Enable: true, Name: "alice", Config: cfg, Inbounds: json.RawMessage(`[]`), Links: json.RawMessage(`[]`)}
	if err := db.Create(&alice).Error; err != nil {
		t.Fatal(err)
	}
	svc := &NodeInboundService{}
	previous := corePtr
	corePtr = core.NewCore()
	t.Cleanup(func() { corePtr = previous })

	// Create: written on the node (no node_id, no users), adopted at once,
	// the named client bound to the replica.
	data := json.RawMessage(`{"id":0,"node_id":99,"type":"vless","tag":"fra-vless","listen":"::","listen_port":8443,"tls_id":5,"users":["x"]}`)
	replica, err := svc.CreateOnNode("admin", node.Id, data, strconv.Itoa(int(alice.Id)), "req-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if replica.NodeId == nil || *replica.NodeId != node.Id || replica.Tag != "fra-vless" {
		t.Fatalf("replica = %+v", replica)
	}
	onNode := f.byTag("fra-vless")
	if onNode == nil || onNode["tls_id"] != float64(5) {
		t.Fatalf("node inbound = %v", onNode)
	}
	if _, ok := onNode["users"]; ok {
		t.Error("users were sent inline to the node")
	}
	var got model.Client
	db.First(&got, alice.Id)
	if !strings.Contains(string(got.Inbounds), strconv.Itoa(int(replica.Id))) {
		t.Errorf("alice inbounds = %s, want replica %d", got.Inbounds, replica.Id)
	}

	// The same submit again is a replay, not a second inbound or an error.
	again, err := svc.CreateOnNode("admin", node.Id, data, "", "req-1")
	if err != nil || again.Id != replica.Id {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if len(f.inbounds) != 1 {
		t.Fatalf("node has %d inbounds after a replay", len(f.inbounds))
	}

	// Taken tag and taken port are refused before anything is written.
	if _, err := svc.CreateOnNode("admin", node.Id, json.RawMessage(`{"type":"vless","tag":"fra-vless","listen_port":9000}`), "", "req-2"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("taken tag: %v", err)
	}
	if _, err := svc.CreateOnNode("admin", node.Id, json.RawMessage(`{"type":"vless","tag":"fra-2","listen_port":8443}`), "", "req-3"); err == nil || !strings.Contains(err.Error(), "fra-vless") {
		t.Errorf("port used by an inbound: %v", err)
	}
	f.mu.Lock()
	f.busy[9443] = "nginx"
	f.mu.Unlock()
	if _, err := svc.CreateOnNode("admin", node.Id, json.RawMessage(`{"type":"hysteria2","tag":"fra-hy2","listen_port":9443}`), "", "req-4"); err == nil || !strings.Contains(err.Error(), "9443/udp") {
		t.Errorf("port bound on the node: %v", err)
	}
	if len(f.inbounds) != 1 {
		t.Fatalf("refused creates wrote to the node: %d inbounds", len(f.inbounds))
	}

	// Form data for the replica: the node's own inbound under the replica id,
	// and the node's TLS list.
	form, err := svc.FormData("admin", 0, replica.Id)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	ib := form["inbound"].(map[string]any)
	if ib["id"] != replica.Id || ib["tls_id"] != float64(5) || form["nodeId"] != node.Id {
		t.Errorf("form = %v", form)
	}
	if !strings.Contains(string(form["tls"].(json.RawMessage)), "node-cert") {
		t.Errorf("tls = %s", form["tls"])
	}

	// Edit: applied on the node under the node's id, replica refreshed.
	edited, err := svc.EditOnNode("admin", json.RawMessage(`{"id":`+strconv.Itoa(int(replica.Id))+`,"node_id":1,"type":"vless","tag":"fra-vless","listen":"::","listen_port":8444}`), "req-5")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if p := f.byTag("fra-vless")["listen_port"]; p != float64(8444) {
		t.Errorf("node port after edit = %v", p)
	}
	if !strings.Contains(string(edited.Options), "8444") {
		t.Errorf("replica options after edit = %s", edited.Options)
	}
	if _, err := svc.EditOnNode("admin", json.RawMessage(`{"id":`+strconv.Itoa(int(replica.Id))+`,"type":"vless","tag":"renamed","listen_port":8444}`), "req-6"); err == nil {
		t.Error("a rename of a node inbound went through")
	}

	// Delete on the node: gone there, replica gone here, client unbound.
	if id, err := svc.DeleteOnNode("admin", "fra-vless", "req-7"); err != nil || id != node.Id {
		t.Fatalf("delete: %v", err)
	}
	if f.byTag("fra-vless") != nil {
		t.Error("still on the node after delete")
	}
	var n int64
	db.Model(model.Inbound{}).Where("tag = ?", "fra-vless").Count(&n)
	if n != 0 {
		t.Error("replica left after delete")
	}
	db.First(&got, alice.Id)
	if strings.Contains(string(got.Inbounds), strconv.Itoa(int(replica.Id))) {
		t.Errorf("alice still bound: %s", got.Inbounds)
	}
}

func TestNodeInboundRefusesOfflineNode(t *testing.T) {
	setupMasterForTest(t)
	f := newTargetNode(t)
	node := model.Node{Name: "off", BaseUrl: f.srv.URL, WebPath: "/app/", Token: "tok", Enable: false, AllowPrivate: true}
	if err := database.GetDB().Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	database.GetDB().Model(&node).Update("enable", false)
	_, err := (&NodeInboundService{}).CreateOnNode("admin", node.Id, json.RawMessage(`{"type":"vless","tag":"x","listen_port":1234}`), "", "")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled node: %v", err)
	}
	var n int64
	database.GetDB().Model(model.Inbound{}).Count(&n)
	if n != 0 {
		t.Error("a replica was made for a refused create")
	}
}
