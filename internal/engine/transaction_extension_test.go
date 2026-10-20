package engine

import (
	"context"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestOwnerExtensionAllocatedWithSession(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	x, _ := s.Transactions.Extension.(*ownerExtension)
	if x == nil {
		t.Fatal("session owner has no root extension")
	}
	if got := ownerExtensionOf(s.Transactions); got != x {
		t.Fatalf("ownerExtensionOf = %p, want the session's extension %p", got, x)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerExtensionOfLiteralOwner(t *testing.T) {
	owner := &session.TransactionOwner{}
	allocs := testing.AllocsPerRun(10, func() {
		if zero := ownerExtensionOrZero(owner); len(zero.foreign.participants) != 0 || zero.cursorCleanup != nil {
			t.Fatal("missing extension does not read as zero")
		}
	})
	if allocs != 0 || owner.Extension != nil {
		t.Fatalf("reading a missing extension allocated (%v allocs, slot %v)", allocs, owner.Extension)
	}
	x := ownerExtensionOf(owner)
	if x == nil || owner.Extension != x || ownerExtensionOf(owner) != x {
		t.Fatal("ownerExtensionOf did not store one extension")
	}
	x.foreign.participants = []*foreignParticipant{{}}
	x.retireForeign()
	if len(x.foreign.participants) != 0 || len(ownerExtensionOrZero(owner).foreign.cleanup) != 1 {
		t.Fatal("retireForeign did not move participants to cleanup")
	}
}
