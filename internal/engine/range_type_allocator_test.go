package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRangeTypeAllocatorFloor(t *testing.T) {
	e := New()
	before := e.Catalog.State
	e.Mu.Lock()
	defer e.Mu.Unlock()
	first, err := e.Catalog.AllocateOIDAtLeastLocked(4000000000)
	if err != nil || first != 4000000000 {
		t.Fatal("floor must skip unused numeric space without changing allocator authority", first, err)
	}
	second, err := e.Catalog.AllocateOIDAtLeastLocked(1)
	if err != nil || second != first+1 {
		t.Fatal("floor recycled an earlier interval", second, err)
	}
	e.Catalog.InstallOIDFrontierLocked(ids.PGCatalogNamespaceOID)
	next, err := e.Catalog.AllocateOIDAtLeastLocked(ids.PGCatalogNamespaceOID)
	if err != nil || next <= ids.PGCatalogNamespaceOID {
		t.Fatal("floor bypassed ordinary reserved/descriptor checks", next, err)
	}
	e.Catalog.InstallOIDFrontierLocked(0)
	if _, err := e.Catalog.AllocateOIDAtLeastLocked(4000000000); !errors.Is(err, catalog.ErrObjectIdentityExhausted) {
		t.Fatal("floor revived exhausted allocator", err)
	}
	if e.Catalog.State != before {
		t.Fatal("allocation published a catalog generation")
	}
}
