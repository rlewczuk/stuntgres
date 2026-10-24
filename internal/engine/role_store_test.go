package engine

import (
	"testing"
)

func TestRoleStoreReadRequiresOwnCatalogAndRoot(t *testing.T) {
	e, other := New(), New()
	view := e.catalog.Current()
	if _, ok := e.roles.PublishedRead(&e.catalog.Owner, view).Lookup("postgres"); !ok {
		t.Fatal("own catalog and root: bootstrap role missing")
	}
	if e.roles.PublishedRead(&other.catalog.Owner, view).BelongsTo(&e.roles.Domain) {
		t.Fatal("view of another catalog owner accepted")
	}
	if e.roles.Read(&e.catalog.Owner, view, nil).BelongsTo(&e.roles.Domain) {
		t.Fatal("nil root accepted")
	}
	if e.roles.Read(&e.catalog.Owner, view, other.roles.Published).BelongsTo(&e.roles.Domain) {
		t.Fatal("root of another domain accepted")
	}
}
