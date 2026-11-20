package bisync

import "testing"

func TestMergeRemote_EchoIgnored(t *testing.T) {
	local := Record{UID: "a", Rev: 2, Base: 1, Origin: "panel-A"}
	remote := Record{UID: "a", Rev: 3, Base: 2, Origin: "panel-A"}
	r := MergeRemote("panel-A", true, local, remote)
	if r.Action != "echo" {
		t.Fatalf("got %s", r.Action)
	}
}

func TestMergeRemote_FastForward(t *testing.T) {
	local := Record{UID: "a", Rev: 2, Base: 1, Origin: "panel-A"}
	remote := Record{UID: "a", Rev: 3, Base: 2, Origin: "panel-B", Payload: map[string]any{"name": "x"}}
	r := MergeRemote("panel-A", true, local, remote)
	if r.Action != "apply" || r.Record.Rev != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestMergeRemote_WeAhead(t *testing.T) {
	local := Record{UID: "a", Rev: 3, Base: 2, Origin: "panel-A"}
	remote := Record{UID: "a", Rev: 2, Base: 1, Origin: "panel-B"}
	r := MergeRemote("panel-A", true, local, remote)
	if r.Action != "noop" {
		t.Fatalf("got %s", r.Action)
	}
}

func TestMergeRemote_Conflict(t *testing.T) {
	local := Record{UID: "a", Rev: 3, Base: 1, Origin: "panel-A"}
	remote := Record{UID: "a", Rev: 3, Base: 1, Origin: "panel-B", Payload: map[string]any{"n": 1}}
	r := MergeRemote("panel-A", true, local, remote)
	if r.Action != "conflict" || r.Conflict == nil {
		t.Fatalf("%+v", r)
	}
}

func TestMergeRemote_Create(t *testing.T) {
	remote := Record{UID: "b", Rev: 1, Base: 0, Origin: "panel-B", Kind: "client"}
	r := MergeRemote("panel-A", false, Record{}, remote)
	if r.Action != "apply" {
		t.Fatalf("%+v", r)
	}
}

func TestMergeRemote_RemoteTombstoneFastForward(t *testing.T) {
	local := Record{UID: "a", Rev: 2, Base: 1, Origin: "panel-A"}
	remote := Record{UID: "a", Rev: 3, Base: 2, Origin: "panel-B", DeletedAt: 100}
	r := MergeRemote("panel-A", true, local, remote)
	if r.Action != "tombstone" {
		t.Fatalf("%+v", r)
	}
}

func TestResolve_KeepLocalAndRemote(t *testing.T) {
	c := Conflict{
		UID:    "a",
		Local:  Record{UID: "a", Rev: 3, Base: 1, Origin: "A"},
		Remote: Record{UID: "a", Rev: 3, Base: 1, Origin: "B"},
	}
	got, ok := Resolve(c, KeepLocal, "A")
	if !ok || got.Action != "apply" || got.Record.Rev < 4 || got.Record.Origin != "A" {
		t.Fatalf("%+v", got)
	}
	got, ok = Resolve(c, KeepRemote, "A")
	if !ok || got.Record.Origin != "B" || got.Record.Rev < 4 {
		t.Fatalf("%+v", got)
	}
	_, ok = Resolve(c, Defer, "A")
	if ok {
		t.Fatal("defer should not apply")
	}
}

func TestBumpAndTombstone(t *testing.T) {
	NowUnix = func() int64 { return 50 }
	defer func() { NowUnix = func() int64 { return 0 } }()
	cur := Record{UID: "a", Rev: 1, Origin: "A"}
	b := BumpRev(cur, "A")
	if b.Rev != 2 || b.Base != 1 {
		t.Fatalf("%+v", b)
	}
	tomb := Tombstone(cur, "A")
	if tomb.DeletedAt != 50 || tomb.Rev != 2 {
		t.Fatalf("%+v", tomb)
	}
}
