package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRangeTypeAllocatorFloor(t *testing.T) {
	e := New()
	before := e.catalog.state
	e.mu.Lock()
	defer e.mu.Unlock()
	first, err := e.catalog.allocateOIDAtLeastLocked(4000000000)
	if err != nil || first != 4000000000 {
		t.Fatal("floor must skip unused numeric space without changing allocator authority", first, err)
	}
	second, err := e.catalog.allocateOIDAtLeastLocked(1)
	if err != nil || second != first+1 {
		t.Fatal("floor recycled an earlier interval", second, err)
	}
	e.catalog.nextOID = ids.PGCatalogNamespaceOID
	next, err := e.catalog.allocateOIDAtLeastLocked(ids.PGCatalogNamespaceOID)
	if err != nil || next <= ids.PGCatalogNamespaceOID {
		t.Fatal("floor bypassed ordinary reserved/descriptor checks", next, err)
	}
	e.catalog.nextOID = 0
	if _, err := e.catalog.allocateOIDAtLeastLocked(4000000000); !errors.Is(err, catalog.ErrObjectIdentityExhausted) {
		t.Fatal("floor revived exhausted allocator", err)
	}
	if e.catalog.state != before {
		t.Fatal("allocation published a catalog generation")
	}
}
