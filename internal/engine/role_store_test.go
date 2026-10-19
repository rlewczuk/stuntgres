package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
)

func TestRoleStoreReadRequiresOwnCatalogAndRoot(t *testing.T) {
	e, other := New(), New()
	view := e.catalog.read(e.catalog.state)
	if _, ok := e.roles.read(e.catalog.owner(), view, e.roles.published).Lookup("postgres"); !ok {
		t.Fatal("own catalog and root: bootstrap role missing")
	}
	if e.roles.read(other.catalog.owner(), view, e.roles.published).BelongsTo(&e.roles.domain) {
		t.Fatal("view of another catalog owner accepted")
	}
	if e.roles.read(e.catalog.owner(), view, nil).BelongsTo(&e.roles.domain) {
		t.Fatal("nil root accepted")
	}
	if e.roles.read(e.catalog.owner(), view, other.roles.published).BelongsTo(&e.roles.domain) {
		t.Fatal("root of another domain accepted")
	}
}

func TestRoleStoreSetReferencesFailuresLeaveStateUnchanged(t *testing.T) {
	e := New()
	ref := e.roles.domain.Principal(authorization.BootstrapPrincipalID)
	if err := e.roles.setReferencesLocked(context.Background(), "kept", []authorization.PrincipalRef{ref}); err != nil {
		t.Fatal(err)
	}
	published, references := e.roles.published, e.roles.references
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.roles.setReferencesLocked(ctx, "kept", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update: %v", err)
	}
	e.roles.published = &roleCatalog{root: published.root, revision: ^uint64(0)}
	exhausted := e.roles.published
	if err := e.roles.setReferencesLocked(context.Background(), "kept", nil); err == nil || err.Error() != "stuntgres: authorization revision exhausted" {
		t.Fatalf("exhausted update: %v", err)
	}
	if e.roles.published != exhausted || len(references) != 1 || len(e.roles.references["kept"]) != 1 {
		t.Fatal("failed update changed the store")
	}
	e.roles.published = published
	if err := e.roles.setReferencesLocked(context.Background(), "added", []authorization.PrincipalRef{ref}); err != nil {
		t.Fatal(err)
	}
	if _, ok := references["added"]; ok {
		t.Fatal("update mutated the previous registry map")
	}
	if e.roles.published.revision != published.revision+1 {
		t.Fatalf("revision %d after %d", e.roles.published.revision, published.revision)
	}
	got := e.roles.referencingFeatures(ref)
	slices.Sort(got)
	if !slices.Equal(got, []string{"added", "kept"}) {
		t.Fatalf("referencing features %v", got)
	}
	if got := e.roles.referencingFeatures(e.roles.domain.Principal(authorization.LastPredefinedRoleID)); len(got) != 0 {
		t.Fatalf("unreferenced role reported by %v", got)
	}
}
