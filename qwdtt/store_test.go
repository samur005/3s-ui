package qwdtt

import (
	"strings"
	"testing"
)

func TestLoadFreshInstallIsDisabledWithDefaults(t *testing.T) {
	initDB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || c.Password != "" || c.ListenAddr != "0.0.0.0:56000" {
		t.Errorf("fresh config = %+v", c)
	}
}

func TestSaveRoundTripGeneratesPassword(t *testing.T) {
	initDB(t)
	in := Default()
	in.Enabled, in.Remark = true, "Home"
	in.VkHashes = "https://vk.ru/call/join/h1, h2"
	in.Clients = []string{" bob ", "bob", "", "alice"}
	saved, err := Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Password) != 16 {
		t.Errorf("password not generated: %q", saved.Password)
	}
	got, _ := Load()
	if got.Password != saved.Password || got.Remark != "Home" || got.VkHashes != "h1,h2" {
		t.Errorf("round trip = %+v", got)
	}
	if strings.Join(got.Clients, ",") != "bob,alice" {
		t.Errorf("clients not cleaned: %v", got.Clients)
	}
	// A second save keeps the password.
	again, _ := Save(got)
	if again.Password != saved.Password {
		t.Error("password changed on re-save")
	}
}

func TestSaveRejectsInvalidAndStoresNothing(t *testing.T) {
	initDB(t)
	bad := Default()
	bad.Workers = 500
	if _, err := Save(bad); err == nil {
		t.Fatal("invalid config saved")
	}
	if c, _ := Load(); c.Workers != 16 {
		t.Errorf("rejected save changed the stored config: %+v", c)
	}
}

func TestRowsWithoutNewKeysKeepTheirDefaults(t *testing.T) {
	// A row written before autoVkHash/routeThroughCore existed.
	c := decodeConfig(`{"enabled":true,"password":"p"}`)
	if !c.AutoVkHash || !c.RouteThroughCore || !c.Enabled {
		t.Errorf("decoded = %+v", c)
	}
	c = decodeConfig(`{"autoVkHash":false,"routeThroughCore":false}`)
	if c.AutoVkHash || c.RouteThroughCore {
		t.Errorf("explicit false was overwritten: %+v", c)
	}
	if c := decodeConfig("not json"); c.ListenAddr != "0.0.0.0:56000" {
		t.Errorf("garbage must decode to defaults: %+v", c)
	}
}

func TestStateSurvivesCookieAndConfigWritesIndependently(t *testing.T) {
	initDB(t)
	enable(t, func(c *Config) { c.Remark = "keep" })
	if err := SaveCookies(goodCookies); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(); c.Remark != "keep" {
		t.Error("saving cookies clobbered the config")
	}
	enable(t, func(c *Config) { c.Remark = "again" })
	if !HasCookies() {
		t.Error("saving the config clobbered the cookies")
	}
}
