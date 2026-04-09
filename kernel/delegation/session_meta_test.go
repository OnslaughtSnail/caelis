package delegation

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func TestChildSessionMetaPrefersExistingChildMeta(t *testing.T) {
	ctx := t.Context()
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "user", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "user", ID: "child"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(ctx, sess); err != nil {
			t.Fatalf("GetOrCreate(%q): %v", sess.ID, err)
		}
	}
	if err := acpmeta.UpdateSessionMeta(ctx, store, parent, func(meta map[string]any) map[string]any {
		return acpmeta.WithModelAlias(meta, "parent-model")
	}); err != nil {
		t.Fatalf("UpdateSessionMeta(parent): %v", err)
	}
	if err := acpmeta.UpdateSessionMeta(ctx, store, child, func(meta map[string]any) map[string]any {
		return acpmeta.WithModelAlias(meta, "child-model")
	}); err != nil {
		t.Fatalf("UpdateSessionMeta(child): %v", err)
	}

	meta := ChildSessionMeta(ctx, store, parent, child, "self")
	if got := acpmeta.ModelAlias(meta); got != "child-model" {
		t.Fatalf("ModelAlias()=%q, want child-model", got)
	}
	if acpmeta.IsDelegatedChild(meta) {
		t.Fatalf("IsDelegatedChild()=true, want false when child metadata already exists")
	}
}

func TestSeedChildSessionMetaCopiesParentMetaForSelf(t *testing.T) {
	ctx := t.Context()
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "user", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "user", ID: "child"}
	if _, err := store.GetOrCreate(ctx, parent); err != nil {
		t.Fatalf("GetOrCreate(parent): %v", err)
	}
	if err := acpmeta.UpdateSessionMeta(ctx, store, parent, func(meta map[string]any) map[string]any {
		return acpmeta.WithModelAlias(meta, "parent-model")
	}); err != nil {
		t.Fatalf("UpdateSessionMeta(parent): %v", err)
	}

	if err := SeedChildSessionMeta(ctx, store, store, parent, child, "self"); err != nil {
		t.Fatalf("SeedChildSessionMeta(): %v", err)
	}

	meta, err := acpmeta.SessionMetaFromStore(ctx, store, child)
	if err != nil {
		t.Fatalf("SessionMetaFromStore(child): %v", err)
	}
	if got := acpmeta.ModelAlias(meta); got != "parent-model" {
		t.Fatalf("ModelAlias()=%q, want parent-model", got)
	}
	if !acpmeta.IsDelegatedChild(meta) {
		t.Fatalf("IsDelegatedChild()=false, want true")
	}
}
