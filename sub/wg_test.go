package sub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/service"
	"gorm.io/gorm"
)

func wgFixture(t *testing.T) (alice, bob uint) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("S3UI_DB_FOLDER", dir)
	if err := database.InitDB(filepath.Join(dir, "sub.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
	mk := func(name string) uint {
		c := model.Client{Name: name, Enable: true, Links: json.RawMessage(`[]`), Inbounds: json.RawMessage(`[]`)}
		if err := database.GetDB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		return c.Id
	}
	alice, bob = mk("alice"), mk("bob")
	save := func(data, users string) {
		err := database.GetDB().Transaction(func(tx *gorm.DB) error {
			_, err := (&service.InboundService{}).Save(tx, "new", json.RawMessage(data), users, "panel.example.com")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	save(`{"type":"wg","tag":"wg","endpoint":"vpn.example.com"}`, "1")
	save(`{"type":"awg","tag":"awg","version":"2.0","endpoint":"vpn.example.com"}`, "1,2")
	return
}

func TestSubscriptionCarriesWireguardLinks(t *testing.T) {
	wgFixture(t)
	s := SubService{}
	res, _, err := s.GetSubs("alice")
	if err != nil {
		t.Fatal(err)
	}
	body := decodeSub(*res)
	if !strings.Contains(body, "wireguard://") || !strings.Contains(body, "vpn://") {
		t.Errorf("alice should get both links:\n%s", body)
	}
	res, _, _ = s.GetSubs("bob")
	body = decodeSub(*res)
	if strings.Contains(body, "wireguard://") || !strings.Contains(body, "vpn://") {
		t.Errorf("bob is bound to the AmneziaWG inbound only:\n%s", body)
	}
	info, err := s.getClientInfoJSON("alice")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range info.Links {
		if strings.HasPrefix(l, "wireguard://") || strings.HasPrefix(l, "vpn://") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the dashboard lists %d WireGuard links, want 2: %v", n, info.Links)
	}
}

func TestSubscriptionConfFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wgFixture(t)
	r := gin.New()
	h := &SubHandler{}
	r.GET("/:subid", h.subs)

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get("/alice?format=conf")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), ".conf") || !strings.Contains(w.Body.String(), "[Interface]") {
		t.Fatalf("conf = %d %v\n%s", w.Code, w.Header(), w.Body.String())
	}
	// Picking the AmneziaWG inbound (id 2) gives its parameters.
	w = get("/alice?format=conf&inbound=2")
	if !strings.Contains(w.Body.String(), "Jc =") {
		t.Errorf("inbound=2:\n%s", w.Body.String())
	}
	w = get("/alice?format=conf&inbound=1")
	if strings.Contains(w.Body.String(), "Jc =") {
		t.Error("inbound=1 is plain WireGuard")
	}
	// A client with no WireGuard binding, or an unknown one, gets no file.
	if w := get("/nobody?format=conf"); w.Code == 200 {
		t.Error("unknown client served")
	}
}
