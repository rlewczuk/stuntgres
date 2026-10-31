package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

// Begin is the real planning operation: an ineligible definition prunes root
// syntax instead of lending its accepted backing to the next attempt.
func foldRootsReusable(owner *core.PreparedFolds) bool {
	if owner == nil || !owner.Live() {
		return false
	}
	current := owner.Current()
	next := owner.Begin()
	for i, entry := range current.Entries {
		if entry.Origin.Owner == (ids.ObjectRef{}) {
			return len(next.Candidate.Entries) == len(current.Entries) && &next.Candidate.Entries[i] == &current.Entries[i]
		}
	}
	return false
}

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
	if foldRootsReusable(owner) {
		t.Fatal("retired root syntax remains reusable")
	}
}
