package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// Exercise the eligibility transition, including empty accepted groups that
// cannot demonstrate reuse by lending a root entry. Call only after all other
// observations: this deliberately retires the definition's next-use rights.
func assertFoldReuseRetirement(t *testing.T, owner *core.PreparedFolds, reusable bool) {
	t.Helper()
	before := owner.Current().Generation
	owner.Invalidate(false)
	want := before
	if reusable {
		want++
	}
	if owner.Current().Generation != want {
		t.Fatal("reuse retirement changed the wrong generation", before, owner.Current().Generation, want)
	}
	if enginetest.FoldRootsReusable(owner) {
		t.Fatal("retired root syntax remains reusable")
	}
}
