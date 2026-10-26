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
