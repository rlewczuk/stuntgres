package session

import (
	"math/rand/v2"

	"stuntgres/internal/engine/ids"
)

// TempTablespaceAssignment is immutable GUC check-hook data. The existing
// settings journal saves this identity, never a consumed selector position.
// It owns only builtin destination OIDs and the transaction that accepted them.
// A later transaction must filter the retained setting again.
type TempTablespaceAssignment struct {
	transaction ids.TxRef
	spaces      []ids.OID
	prepared    bool
}

// NewTempTablespaceAssignment takes ownership of spaces, prepared by core before
// the final setting-publication fence. No caller may subsequently mutate it.
func NewTempTablespaceAssignment(tx ids.TxRef, spaces []ids.OID) *TempTablespaceAssignment {
	return &TempTablespaceAssignment{transaction: tx, spaces: spaces, prepared: true}
}

func cloneTempTablespaceAssignment(a *TempTablespaceAssignment, value string) *TempTablespaceAssignment {
	if a == nil {
		// An untouched empty setting has no accepted list, phase or ACL state
		// to detach. Preserve its nil journal image and avoid an allocation.
		if value == "" {
			return nil
		}
		// Retained startup text still needs a distinct effective-owner key,
		// but has no interactive check-hook data to reuse, even at TxRef zero.
		return &TempTablespaceAssignment{}
	}
	copy := *a
	return &copy
}

// AcceptedTempTablespaces returns immutable check-hook data only in the same
// transaction. It does not interpret retained text or capture authority.
func (op *Operation) AcceptedTempTablespaces(tx ids.TxRef) ([]ids.OID, bool) {
	a := op.TempTablespaceAssignment
	if a == nil || !a.prepared || a.transaction != tx {
		return nil, false
	}
	return a.spaces, true
}

// TempTablespaceSelector is serialized by physical-owner statement admission.
// Savepoint rollback does not rewind consumption, including a failed CREATE.
// Neither catalog/authorization graphs nor effective SettingsStores live here.
type TempTablespaceSelector struct {
	transaction ids.TxRef
	assignment  *TempTablespaceAssignment
	spaces      []ids.OID
	next        int
	initialized bool
}

func (s *TempTablespaceSelector) Current(tx ids.TxRef, assignment *TempTablespaceAssignment) bool {
	return s.initialized && s.transaction == tx && s.assignment == assignment
}

// Install chooses one initial phase independently of SQL random()/setseed().
// spaces is already owned and immutable; installation never copies.
func (s *TempTablespaceSelector) Install(tx ids.TxRef, assignment *TempTablespaceAssignment, spaces []ids.OID) {
	start := 0
	if len(spaces) > 1 {
		start = rand.IntN(len(spaces))
	}
	*s = TempTablespaceSelector{transaction: tx, assignment: assignment, spaces: spaces, next: start, initialized: true}
}


func (s *TempTablespaceSelector) Next() ids.OID {
	if len(s.spaces) == 0 {
		return 0
	}
	space := s.spaces[s.next]
	s.next++
	if s.next == len(s.spaces) {
		s.next = 0
	}
	return space
}

// AcceptTempTablespacesLocked follows successful ApplyLocked under its existing
// publication locks. The candidate was fully prepared before final preflight.
func (s *SettingsStore) AcceptTempTablespacesLocked(a *TempTablespaceAssignment) {
	s.TempTablespaces = a
	if s.Session != nil && s.Session.Transactions != nil {
		t := s.Session.Transactions
		if a == nil {
			t.TempTablespaces = TempTablespaceSelector{}
		} else {
			t.TempTablespaces.Install(t.Ref, a, a.spaces)
		}
	}
}

// restoreTempTablespaces invalidates only an actual GUC restoration. Ordinary
// failed-creation rollback with an unchanged token must leave the cursor alone.
func (s *SettingsStore) restoreTempTablespaces(a *TempTablespaceAssignment) {
	if s.TempTablespaces != a {
		s.TempTablespaces = a
		if s.Session != nil && s.Session.Transactions != nil {
			s.Session.Transactions.TempTablespaces = TempTablespaceSelector{}
		}
	}
}
