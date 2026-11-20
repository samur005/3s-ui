package qwdtt

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.ListenAddr != "0.0.0.0:56000" || c.WGPort != 56001 || c.ClientPort != 9000 || c.Workers != 16 {
		t.Errorf("defaults changed: %+v", c)
	}
	if !c.RouteThroughCore || !c.AutoVkHash || c.AccessMode != AccessAll {
		t.Errorf("routing, auto hash and access must default on: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		ok   bool
	}{
		{"default", func(*Config) {}, true},
		{"listen without port", func(c *Config) { c.ListenAddr = "0.0.0.0" }, false},
		{"wg port zero", func(c *Config) { c.WGPort = 0 }, false},
		{"wg port high", func(c *Config) { c.WGPort = 70000 }, false},
		{"empty dns", func(c *Config) { c.DNS = " " }, false},
		{"dns with port", func(c *Config) { c.DNS = "8.8.8.8:53" }, false},
		{"ipv6 dns", func(c *Config) { c.DNS = "2001:4860:4860::8888" }, true},
		{"bad listenRaw", func(c *Config) { c.ListenRaw = "nope" }, false},
		{"empty listenRaw", func(c *Config) { c.ListenRaw = "" }, true},
		{"bad listenDirect", func(c *Config) { c.ListenDirect = "1.2.3.4" }, false},
		{"bad subHost", func(c *Config) { c.SubHost = "example.com" }, false},
		{"good subHost", func(c *Config) { c.SubHost = "example.com:56000" }, true},
		{"client port zero", func(c *Config) { c.ClientPort = 0 }, false},
		{"workers zero", func(c *Config) { c.Workers = 0 }, false},
		{"workers 65", func(c *Config) { c.Workers = 65 }, false},
		{"tag with newline", func(c *Config) { c.OutboundTag = "a\nb" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			err := c.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestMergeFillsZeroFields(t *testing.T) {
	got := Config{Remark: "x"}.Merge()
	d := Default()
	if got.ListenAddr != d.ListenAddr || got.WGPort != d.WGPort || got.DNS != d.DNS ||
		got.ClientPort != d.ClientPort || got.Workers != d.Workers || got.AccessMode != AccessAll {
		t.Errorf("Merge did not fill defaults: %+v", got)
	}
	if got.Remark != "x" {
		t.Errorf("Merge overwrote a set field")
	}
}

func TestBuildArgs(t *testing.T) {
	c := Default()
	c.ConfigDir = t.TempDir()
	c.Password = "secret"
	c.ListenDirect = "0.0.0.0:56004"
	want := []string{
		"-listen", "0.0.0.0:56000", "-wg-port", "56001", "-config-dir", c.ConfigDir,
		"-password", "secret", "-dns", "8.8.8.8", "-listen-raw", "0.0.0.0:56003",
		"-listen-direct", "0.0.0.0:56004",
	}
	if got := c.BuildArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("BuildArgs:\n got %v\nwant %v", got, want)
	}
	c.ListenRaw, c.ListenDirect, c.Password = "", "", ""
	got := strings.Join(c.BuildArgs(), " ")
	for _, bad := range []string{"-listen-raw", "-listen-direct", "-password"} {
		if strings.Contains(got, bad) {
			t.Errorf("empty option still passed: %s in %q", bad, got)
		}
	}
}

func TestResolveConfigDirDefaultsUnderDBFolder(t *testing.T) {
	t.Setenv("S3UI_DB_FOLDER", "/data/3s-ui")
	if got := (Config{}).ResolveConfigDir(); got != "/data/3s-ui/qwdtt" {
		t.Errorf("ResolveConfigDir = %q", got)
	}
}

func TestEnsurePassword(t *testing.T) {
	c, err := Config{}.EnsurePassword()
	if err != nil || len(c.Password) != 16 {
		t.Fatalf("EnsurePassword = %q, %v", c.Password, err)
	}
	if strings.ContainsAny(c.Password, "0O1lI") {
		t.Errorf("look-alike characters in %q", c.Password)
	}
	again, _ := c.EnsurePassword()
	if again.Password != c.Password {
		t.Error("an existing password was replaced")
	}
	other, _ := Config{}.EnsurePassword()
	if other.Password == c.Password {
		t.Error("two generated passwords collided")
	}
}

func TestWithPeerHostAndDTLSPort(t *testing.T) {
	c := Default()
	c.ListenAddr = "0.0.0.0:443"
	if got := c.WithPeerHost(" 1.2.3.4 ").SubHost; got != "1.2.3.4:443" {
		t.Errorf("SubHost = %q", got)
	}
	if got := c.WithPeerHost("").SubHost; got != "" {
		t.Errorf("empty host must be a no-op, got %q", got)
	}
	if got := c.WithPeerHost("2001:db8::1").SubHost; got != "[2001:db8::1]:443" {
		t.Errorf("IPv6 host = %q", got)
	}
	c.ListenAddr = "garbage"
	if c.DTLSPort() != 56000 {
		t.Error("unreadable listen address must fall back to 56000")
	}
}

func TestClientURI(t *testing.T) {
	t.Setenv(EnvVkHash, "")
	c := Default()
	if c.ClientURI() != "" {
		t.Error("no peer, no password: link must be empty")
	}
	c.SubHost, c.Password, c.Remark = "1.2.3.4:56000", "secret", "Home"
	c.VkHashes = "h1,h2"
	got := c.ClientURI()
	if !strings.HasPrefix(got, "qwdtt://config?") || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("not a one-line qwdtt://config? link: %q", got)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(got, "qwdtt://config?"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"name": "Home", "peer": "1.2.3.4:56000", "hashes": "h1,h2", "workers": "16", "port": "9000", "pass": "secret",
	} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	for _, want := range []string{"peer=1.2.3.4%3A56000", "hashes=h1%2Ch2"} {
		if !strings.Contains(got, want) {
			t.Errorf("link missing %q: %s", want, got)
		}
	}
	c.Remark, c.VkHashes = "", ""
	got = c.ClientURI()
	if !strings.Contains(got, "name=qWDTT") || strings.Contains(got, "hashes=") {
		t.Errorf("default name or empty hashes wrong: %s", got)
	}
}

func TestClientURIEnvHashOverride(t *testing.T) {
	c := Default()
	c.SubHost, c.Password, c.VkHashes = "1.2.3.4:56000", "p", "stored"
	t.Setenv(EnvVkHash, "https://vk.ru/call/join/envhash, other")
	got := c.ClientURI()
	if !strings.Contains(got, "hashes=envhash%2Cother") || strings.Contains(got, "stored") {
		t.Errorf("environment hashes must win: %s", got)
	}
}

func TestSubscription(t *testing.T) {
	t.Setenv(EnvVkHash, "")
	c := Default()
	if _, err := c.Subscription(); err == nil {
		t.Error("subscription without peer must fail")
	}
	c.SubHost = "1.2.3.4:56000"
	if _, err := c.Subscription(); err == nil {
		t.Error("subscription without password must fail")
	}
	c.Password, c.VkHashes, c.Remark = "pw", "h1", "Home"
	s, err := c.Subscription()
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 1 || s.SubscriptionName != "Home" || len(s.Profiles) != 1 {
		t.Fatalf("subscription = %+v", s)
	}
	p := s.Profiles[0]
	if p.Peer != "1.2.3.4:56000" || p.Hashes != "h1" || p.Password != "pw" || p.Port != 9000 || p.Workers != 16 {
		t.Errorf("profile = %+v", p)
	}
}

func TestHasAccess(t *testing.T) {
	c := Default()
	if !c.HasAccess("alice") {
		t.Error("mode all must give everyone access")
	}
	c.AccessMode, c.Clients = AccessSelected, []string{"alice"}
	if !c.HasAccess("alice") || c.HasAccess("bob") {
		t.Error("mode selected must give access to the listed clients only")
	}
	c.Clients = nil
	if c.HasAccess("alice") {
		t.Error("mode selected with no clients must give nobody access")
	}
}

func TestHashParsing(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"abc":                               "abc",
		"https://vk.com/call/join/h1, h2":   "h1,h2",
		"https://vk.ru/call/join/h1?x=1":    "h1",
		"https://m.vk.ru/call/join/h1/":     "h1",
		"https://vk.me/join/h1":             "h1",
		"h1;h1\nh2":                         "h1,h2",
		"https://example.com/other":         "",
		"a,b,c,d,e,f":                       "a,b,c,d",
		" http://vk.com/call/join/AbC#frag": "AbC",
	}
	for in, want := range cases {
		if got := NormalizeHashes(in); got != want {
			t.Errorf("NormalizeHashes(%q) = %q, want %q", in, got, want)
		}
	}
}
