package visibility

import (
	"math"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestRegistryCommitSeqAdvanceAndSeed(t *testing.T) {
	reg := NewRegistry(false)
	if reg.CommitSeq() != 0 {
		t.Fatal("fresh registry has a commit frontier", reg.CommitSeq())
	}
	reg.AdvanceCommitSeqLocked()
	if reg.CommitSeq() != 1 {
		t.Fatal("advance is not one step", reg.CommitSeq())
	}
	reg.SetCommitSeqLocked(10)
	reg.AdvanceCommitSeqLocked()
	if reg.CommitSeq() != 11 {
		t.Fatal("advance did not continue from the seeded frontier", reg.CommitSeq())
	}
}

func TestNextFullXIDSkipsSpecialLowValues(t *testing.T) {
	cases := []struct{ last, next uint64 }{
		{2, 3},
		{3, 4},
		{1<<32 - 1, 1<<32 | 3},
		{1<<32 | 3, 1<<32 | 4},
		{math.MaxUint64 - 1, math.MaxUint64},
	}
	for _, c := range cases {
		if next, err := NextFullXID(c.last); err != nil || next != c.next {
			t.Fatalf("NextFullXID(%d) = %d, %v; want %d", c.last, next, err, c.next)
		}
	}
	if _, err := NextFullXID(math.MaxUint64); err == nil {
		t.Fatal("NextFullXID did not report exhaustion")
	}
}

// Assignment is monotonic in full XIDs, and a new assignment never reuses the
// low 32 bits of a transaction that is still in progress.
func TestRegistryXIDAssignmentLiveAliasGuard(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.tx(), f.tx(), f.tx()
	if err := f.reg.AssignTransactionXIDLocked(a); err != nil {
		t.Fatal(err)
	}
	if f.reg.FullXIDLocked(a) != 3 || f.reg.AssignedXIDLocked(a) != 3 || f.reg.LastAssignedXID() != 3 {
		t.Fatal("first assignment is not XID 3", f.reg.FullXIDLocked(a), f.reg.LastAssignedXID())
	}
	if err := f.reg.AssignTransactionXIDLocked(a); err != nil || f.reg.LastAssignedXID() != 3 {
		t.Fatal("reassignment advanced the frontier", err, f.reg.LastAssignedXID())
	}
	// The next candidate aliases a's low 32 bits while a is in progress.
	f.reg.SetLastAssignedXIDLocked(1<<32 | 2)
	if err := f.reg.AssignTransactionXIDLocked(b); err == nil {
		t.Fatal("assignment aliased a live transaction")
	}
	if f.reg.FullXIDLocked(b) != 0 || f.reg.LastAssignedXID() != 1<<32|2 {
		t.Fatal("refused assignment changed state", f.reg.FullXIDLocked(b), f.reg.LastAssignedXID())
	}
	f.reg.EndTransactionLocked(a, true)
	if err := f.reg.AssignTransactionXIDLocked(b); err != nil {
		t.Fatal("ended transaction still blocks its alias", err)
	}
	if f.reg.FullXIDLocked(b) != 1<<32|3 || f.reg.AssignedXIDLocked(b) != 3 {
		t.Fatal("alias assignment has the wrong identity", f.reg.FullXIDLocked(b))
	}
	if err := f.reg.AssignTransactionXIDLocked(c); err != nil || f.reg.FullXIDLocked(c) != 1<<32|4 {
		t.Fatal("assignment is not monotonic", err, f.reg.FullXIDLocked(c))
	}
	if status, ok, err := f.reg.TransactionStatusLocked(3); err != nil || !ok || status != "committed" {
		t.Fatal("ended identity lost its status", status, ok, err)
	}
}

func TestLiveVersionOverlayAndEnds(t *testing.T) {
	row := storage.RowVersion{Row: 1, Version: 10, Born: 5}
	ends := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 10, End: 8}}}
	cases := []struct {
		name     string
		v        storage.RowVersion
		frontier ids.CommitSeq
		ends     *storage.DeletionRoot
		own      *PrivateDeletes
		live     bool
	}{
		{"private insert", storage.RowVersion{Row: 1, Version: 10}, 0, nil, nil, true},
		{"born after frontier", row, 4, nil, nil, false},
		{"born at frontier", row, 5, nil, nil, true},
		{"ended after frontier", row, 7, ends, nil, true},
		{"ended at frontier", row, 8, ends, nil, false},
		{"end of another version", storage.RowVersion{Row: 1, Version: 11, Born: 5}, 9, ends, nil, true},
		{"own delete", row, 7, nil, &PrivateDeletes{Rows: map[ids.RowID]DeleteCandidate{1: {Row: 1, Version: 10}}}, false},
		{"own replacement base", row, 7, nil, &PrivateDeletes{Rows: map[ids.RowID]DeleteCandidate{1: {Row: 1, Version: 12, Base: 10}}}, false},
		{"own delete of another lineage", row, 7, nil, &PrivateDeletes{Rows: map[ids.RowID]DeleteCandidate{1: {Row: 1, Version: 12}}}, true},
	}
	for _, c := range cases {
		if got := LiveVersion(c.v, c.frontier, c.ends, c.own); got != c.live {
			t.Errorf("%s: LiveVersion = %v, want %v", c.name, got, c.live)
		}
	}
}
