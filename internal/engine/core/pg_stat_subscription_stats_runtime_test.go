package core

import "testing"

// This constructor-only unit has no engine, session, locks or SQL execution.
func TestPGStatSubscriptionStatsRuntimeBorrow(t *testing.T) {
	ordinary := newCatalogRuntime(CatalogRuntimeFields{})
	if ordinary.Host.(*catalogRuntime).engineLocked {
		t.Fatal("standalone construction borrowed an engine lock")
	}
	borrowed := newCatalogRuntime(CatalogRuntimeFields{engineLocked: true})
	if !borrowed.Host.(*catalogRuntime).engineLocked {
		t.Fatal("synchronous capture lost its actual lock context")
	}
	fields := CatalogRuntimeFieldsOf(borrowed)
	if fields.engineLocked {
		t.Fatal("inspection exported lexical lock authority")
	}
	reconstructed := newCatalogRuntime(fields)
	if reconstructed.Host.(*catalogRuntime).engineLocked {
		t.Fatal("inspection roundtrip replayed an expired lock borrow")
	}
}
