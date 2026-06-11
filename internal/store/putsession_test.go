package store

import "testing"

// ---------------------------------------------------------------------------
// F22 — the 8-char ShortID map key is only 32 bits of entropy, so two distinct
// full IDs of the same agent can collide on the short key. PutSession is a
// guarded insert that refuses to silently overwrite an existing record whose
// full ID differs from the incoming one.
// ---------------------------------------------------------------------------

func TestPutSessionRefusesShortKeyCollisionWithDifferentFullID(t *testing.T) {
	cfg := DefaultConfig()
	// Two distinct full UUIDs that share the same 8-char prefix collide on the
	// short key Key() derives.
	idA := "abcdef12-aaaa-1111-aaaa-111111111111"
	idB := "abcdef12-bbbb-2222-bbbb-222222222222"
	keyA := Key("claude", idA)
	keyB := Key("claude", idB)
	if keyA != keyB {
		t.Fatalf("test precondition: expected colliding short keys, got %q vs %q", keyA, keyB)
	}

	recA := SessionRecord{ID: idA, Agent: "claude", SessionName: "uam-claude-a"}
	recB := SessionRecord{ID: idB, Agent: "claude", SessionName: "uam-claude-b"}

	if !cfg.PutSession(keyA, recA) {
		t.Fatalf("first PutSession must succeed (no existing record)")
	}
	// A collision on the short key but a DIFFERENT full ID must be refused so the
	// distinct session A is not silently clobbered.
	if cfg.PutSession(keyB, recB) {
		t.Fatalf("PutSession must refuse to overwrite a record with a different full ID")
	}
	if got := cfg.Sessions[keyA].ID; got != idA {
		t.Fatalf("existing record clobbered: ID = %q, want %q", got, idA)
	}
}

func TestPutSessionUpdatesSameFullID(t *testing.T) {
	cfg := DefaultConfig()
	id := "abcdef12-aaaa-1111-aaaa-111111111111"
	key := Key("claude", id)
	if !cfg.PutSession(key, SessionRecord{ID: id, Agent: "claude", Name: "old"}) {
		t.Fatalf("initial insert must succeed")
	}
	// Same full ID -> an in-place update of the live<->stored join must succeed.
	if !cfg.PutSession(key, SessionRecord{ID: id, Agent: "claude", Name: "new"}) {
		t.Fatalf("PutSession with the same full ID must succeed (update)")
	}
	if got := cfg.Sessions[key].Name; got != "new" {
		t.Fatalf("record not updated: Name = %q, want %q", got, "new")
	}
}

func TestShortKeyJoinsLiveSessionToFullUUIDStoredRecord(t *testing.T) {
	// The short key must remain the join between a live session (keyed by its
	// full UUID via Key) and the stored record. PutSession must not break that:
	// re-deriving the key from the same full UUID resolves the stored record.
	cfg := DefaultConfig()
	fullID := "abcdef12-aaaa-1111-aaaa-111111111111"
	key := Key("claude", fullID)
	rec := SessionRecord{ID: fullID, Agent: "claude", SessionName: "uam-claude-x"}
	if !cfg.PutSession(key, rec) {
		t.Fatalf("PutSession must succeed")
	}
	// A live session reporting the same full UUID derives the same short key.
	got, ok := cfg.Sessions[Key("claude", fullID)]
	if !ok {
		t.Fatalf("short key did not join live session to stored record")
	}
	if got.ID != fullID {
		t.Fatalf("joined record has wrong full ID: %q", got.ID)
	}
}
