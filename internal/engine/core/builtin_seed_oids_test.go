package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The builtin seed allocates OIDs without walking definitions. That is sound
// only while every seeded definition OID is below the allocator's frontier and
// distinct from every other catalog identity.
func TestBuiltinSeedDefinitionOIDs(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	used := make(map[ids.OID]bool)
	for _, d := range view.Descriptors() {
		catalog.ReserveDefinitionOIDs(used, d)
	}
	next, allocated := e.Catalog.NextOID(), 0
	for id := range used {
		if id >= next {
			t.Fatalf("seeded definition OID %d is at or past the frontier %d", id, next)
		}
		if id >= 16384 {
			allocated++
		}
	}
	if allocated == 0 {
		t.Fatal("seed allocated no definition OIDs")
	}
}

// Initial privileges visit the builtin namespaces instead of every seeded
// descriptor. That is complete only while the seed installs exactly those.
func TestBuiltinSeedNamespaces(t *testing.T) {
	view := engine.New().CatalogRead()
	want := make(map[ids.ObjectAddress]bool)
	for address := range catalog.BuiltinNamespaces() {
		want[address] = true
	}
	for address, d := range view.Descriptors() {
		if _, ok := d.(*catalog.Namespace); !ok {
			continue
		}
		if !want[address] {
			t.Errorf("seeded namespace %v is not a catalog.BuiltinNamespaces entry", address)
		}
		delete(want, address)
	}
	for address := range want {
		t.Errorf("builtin namespace %v is not seeded", address)
	}
}
