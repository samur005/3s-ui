package xray

import "testing"

func TestDeriveClientPassStable(t *testing.T) {
	a := DeriveClientPass("master", "alice")
	b := DeriveClientPass("master", "alice")
	c := DeriveClientPass("master", "bob")
	if a == "" || a != b {
		t.Fatalf("stable %q %q", a, b)
	}
	if a == c {
		t.Fatal("different clients must differ")
	}
}

func TestClientSocksList(t *testing.T) {
	list := clientSocksList(3, "master", []ClientCred{
		{Email: "alice", ID: "1", Enable: true},
		{Email: "bob", ID: "2", Enable: true},
		{Email: "alice", ID: "3", Enable: true}, // dup name
		{Email: "off", ID: "4", Enable: false},
	})
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	if list[0].Username != "alice" || list[1].Username != "bob" {
		t.Fatalf("%+v", list)
	}
	if list[0].Password == list[1].Password {
		t.Fatal("passwords should differ")
	}
}
