package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/visibility"
)

func accessMethodHeapOperationsUnit(t *testing.T, view catalog.Read) visibility.HeapOperations {
	t.Helper()
	heap, err := visibility.ResolveHeapAccess(view, 2)
	if err != nil {
		t.Fatal(err)
	}
	return heap
}
