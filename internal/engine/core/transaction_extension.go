package core

import (
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

// ownerExtension is the root-owned part of a transaction owner (13c decision
// 3): foreign transaction participants and cleanup, exclusion constraint modes
// and the SQL cursors detached for cleanup. startSession allocates it with the
// session; its fields keep the owner's admission and owner-lock rules.
type ownerExtension struct {
	foreign         fdw.Transaction
	constraintModes index.ConstraintModes
	cursorCleanup   []*sqlCursor // Detached under owner lock; drained unlocked.
}

func (*ownerExtension) OwnerSlot() {}

// ownerExtensionOf returns t's extension for writing. Session owners always
// have one; an owner built without one (unit-test literals) gets one here.
func ownerExtensionOf(t *session.TransactionOwner) *ownerExtension {
	x, _ := t.Extension.(*ownerExtension)
	if x == nil {
		x = new(ownerExtension)
		t.Extension = x
	}
	return x
}

// ownerExtensionOrZero returns a copy of t's extension for reading; a missing
// extension reads as all zero and is not allocated.
func ownerExtensionOrZero(t *session.TransactionOwner) ownerExtension {
	if x, _ := t.Extension.(*ownerExtension); x != nil {
		return *x
	}
	return ownerExtension{}
}

// retireForeign moves the foreign participants to cleanup.
func (x *ownerExtension) retireForeign() { x.foreign.Retire() }

// ownerHasForeign reports whether t has enlisted foreign participants.
func ownerHasForeign(t *session.TransactionOwner) bool {
	foreign := ownerExtensionOrZero(t).foreign
	return foreign.HasParticipants()
}

// detachCursors queues cursors for cleanup outside the owner lock.
func (x *ownerExtension) detachCursors(cursors []*sqlCursor) {
	x.cursorCleanup = append(x.cursorCleanup, cursors...)
}
