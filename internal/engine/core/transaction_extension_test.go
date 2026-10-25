package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
)

func TestOwnerExtensionAllocatedWithSession(t *testing.T) {
	e := engine.NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	x, _ := s.Transactions.Extension.(*core.OwnerExtension)
	if x == nil {
		t.Fatal("session owner has no root extension")
	}
	if got := core.OwnerExtensionOf(s.Transactions); got != x {
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
		if zero := core.OwnerExtensionOrZero(owner); core.OwnerExtensionForeignRef(&zero).HasParticipants() || core.OwnerExtensionCursorCleanup(&zero) != nil {
			t.Fatal("missing extension does not read as zero")
		}
	})
	if allocs != 0 || owner.Extension != nil {
		t.Fatalf("reading a missing extension allocated (%v allocs, slot %v)", allocs, owner.Extension)
	}
	x := core.OwnerExtensionOf(owner)
	if x == nil || owner.Extension != x || core.OwnerExtensionOf(owner) != x {
		t.Fatal("ownerExtensionOf did not store one extension")
	}
	core.OwnerExtensionForeignRef(x).Enlist(&fdw.Participant{})
	core.OwnerExtensionRetireForeign(x)
	if core.OwnerExtensionForeignRef(x).HasParticipants() || len(core.OwnerExtensionForeignRef(core.OwnerExtensionOf(owner)).TakeCleanup()) != 1 {
		t.Fatal("retireForeign did not move participants to cleanup")
	}
}
