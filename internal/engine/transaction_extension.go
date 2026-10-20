package engine

// ownerExtension is the root-owned part of a transaction owner (13c decision
// 3): foreign transaction participants and cleanup, exclusion constraint modes
// and the SQL cursors detached for cleanup. startSession allocates it with the
// session; its fields keep the owner's admission and owner-lock rules.
type ownerExtension struct {
	foreign        foreignTransaction
	exclusionModes exclusionModes
	cursorCleanup  []*sqlCursor // Detached under owner lock; drained unlocked.
}

func (*ownerExtension) OwnerSlot() {}

// ownerExtensionOf returns t's extension for writing. Session owners always
// have one; an owner built without one (unit-test literals) gets one here.
func ownerExtensionOf(t *transactionOwner) *ownerExtension {
	x, _ := t.Extension.(*ownerExtension)
	if x == nil {
		x = new(ownerExtension)
		t.Extension = x
	}
	return x
}

// ownerExtensionOrZero returns a copy of t's extension for reading; a missing
// extension reads as all zero and is not allocated.
func ownerExtensionOrZero(t *transactionOwner) ownerExtension {
	if x, _ := t.Extension.(*ownerExtension); x != nil {
		return *x
	}
	return ownerExtension{}
}

// retireForeign moves the foreign participants to cleanup.
func (x *ownerExtension) retireForeign() {
	x.foreign.cleanup = append(x.foreign.cleanup, x.foreign.participants...)
	x.foreign.participants = nil
}

// detachCursors queues cursors for cleanup outside the owner lock.
func (x *ownerExtension) detachCursors(cursors []*sqlCursor) {
	x.cursorCleanup = append(x.cursorCleanup, cursors...)
}
