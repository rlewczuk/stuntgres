package visibility

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestLogicalChildIdentity(t *testing.T) {
	f := newFixture(t)
	reg := NewRegistry(true)
	ref := f.tx()
	if err := reg.PreflightXIDsLocked(3); err != nil {
		t.Fatal(err)
	}
	if err := reg.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	top := reg.FullXIDLocked(ref)
	child := reg.AssignChildXIDLocked(top)
	grandchild := reg.AssignChildXIDLocked(child)
	if top != 3 || child != 4 || grandchild != 5 || len(reg.Entries) != 1 || reg.History[child].Ticket != nil || reg.History[grandchild].Parent != child {
		t.Fatal("parallel identity owner", reg)
	}
	reg.ReleaseChildLocked(grandchild, child)
	if !reg.History[grandchild].Subcommitted {
		t.Fatal("actual RELEASE not retained")
	}
	for _, xid := range []uint64{top, child, grandchild} {
		status, ok, err := reg.TransactionStatusLocked(xid)
		if err != nil || !ok || status != "in progress" {
			t.Fatal(xid, status, ok, err)
		}
	}
	// Subcommit retains ancestry; rolling back its parent must abort both.
	reg.AbortChildLocked(child)
	for _, xid := range []uint64{child, grandchild} {
		status, _, _ := reg.TransactionStatusLocked(xid)
		if status != "aborted" {
			t.Fatal("subtree survived", xid, status)
		}
	}
	if err := reg.PreflightXIDsLocked(1); err != nil {
		t.Fatal(err)
	}
	survivor := reg.AssignChildXIDLocked(top)
	reg.RecordCommitTimestampLocked(ref, 12345, 7)
	if reg.History[top].State != ids.TxCommitted || reg.History[survivor].State != ids.TxCommitted || reg.History[child].State != ids.TxAborted {
		t.Fatal("completion ancestry lost", reg.History)
	}
	if stamp := reg.LookupCommitTimestampLocked(uint32(survivor)); !stamp.Present || stamp.Stamp != 12345 || stamp.Origin != 7 {
		t.Fatal("child commit timestamp", stamp)
	}
	if reg.LookupCommitTimestampLocked(uint32(child)).Present {
		t.Fatal("aborted child acquired commit timestamp")
	}
}
func TestLogicalChildAssignmentPreflight(t *testing.T) {
	f := newFixture(t)
	ref := f.tx()
	reg := NewRegistry(false)
	reg.assignedXID = ^uint64(0) - 1
	before := reg
	if err := reg.PreflightXIDsLocked(2); err == nil || !reflect.DeepEqual(reg, before) {
		t.Fatal("partial exhausted stack assignment", err)
	}
	reg = NewRegistry(false)
	if err := reg.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	child := reg.AssignChildXIDLocked(reg.FullXIDLocked(ref))
	reg.assignedXID = 1<<32 | uint64(uint32(child)-1)
	frontier := reg.assignedXID
	if err := reg.PreflightXIDsLocked(1); err == nil || reg.assignedXID != frontier {
		t.Fatal("live child low alias accepted", err)
	}
	reg.AbortChildLocked(child)
	if err := reg.PreflightXIDsLocked(1); err != nil {
		t.Fatal("completed child alias refused", err)
	}
	reg.RetireCommitTimestampsLocked()
	if len(reg.children) != 0 || len(reg.History) != 0 || len(reg.LowIDs) != 0 {
		t.Fatal("replacement retained child owners")
	}
}
