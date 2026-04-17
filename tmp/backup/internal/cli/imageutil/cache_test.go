package imageutil

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestCache_PutGet(t *testing.T) {
	c := NewCache(4)
	part := model.ContentPart{Type: model.ContentPartImage, Data: "abc123", FileName: "test.png"}
	c.Put("key1", part)

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Data != "abc123" {
		t.Fatalf("unexpected data: %q", got.Data)
	}
}

func TestCache_Miss(t *testing.T) {
	c := NewCache(4)
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCache_Eviction(t *testing.T) {
	c := NewCache(2)
	c.Put("a", model.ContentPart{Data: "1"})
	c.Put("b", model.ContentPart{Data: "2"})

	// Access "a" to make it more recently used.
	c.Get("a")

	// Insert "c" — should evict "b" (least recently used).
	c.Put("c", model.ContentPart{Data: "3"})

	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected 'a' to survive eviction")
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected 'c' to be present")
	}
}

func TestCache_UpdateExisting(t *testing.T) {
	c := NewCache(4)
	c.Put("k", model.ContentPart{Data: "old"})
	c.Put("k", model.ContentPart{Data: "new"})

	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Data != "new" {
		t.Fatalf("expected updated value, got %q", got.Data)
	}
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", c.Len())
	}
}

func TestKey_Deterministic(t *testing.T) {
	data := []byte("hello world")
	k1 := Key(data)
	k2 := Key(data)
	if k1 != k2 {
		t.Fatalf("expected same key, got %q and %q", k1, k2)
	}
	if k1 == "" {
		t.Fatal("key should not be empty")
	}
}

func TestKey_DifferentData(t *testing.T) {
	k1 := Key([]byte("hello"))
	k2 := Key([]byte("world"))
	if k1 == k2 {
		t.Fatal("different data should produce different keys")
	}
}
