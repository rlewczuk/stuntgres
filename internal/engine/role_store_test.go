package engine

import (
	"testing"
)

func TestRoleStoreReadRequiresOwnCatalogAndRoot(t *testing.T) {
	e, other := New(), New()
	view := e.catalog.Current()
	if _, ok := e.roles.read(&e.catalog.Owner, view, e.roles.published).Lookup("postgres"); !ok {
		t.Fatal("own catalog and root: bootstrap role missing")
	}
	if e.roles.read(&other.catalog.Owner, view, e.roles.published).BelongsTo(&e.roles.domain) {
		t.Fatal("view of another catalog owner accepted")
	}
	if e.roles.read(&e.catalog.Owner, view, nil).BelongsTo(&e.roles.domain) {
		t.Fatal("nil root accepted")
	}
	if e.roles.read(&e.catalog.Owner, view, other.roles.published).BelongsTo(&e.roles.domain) {
		t.Fatal("root of another domain accepted")
	}
}
