package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func snapshotActionOwners(t *testing.T, state *State, parent ids.ObjectAddress, want ...*Relation) {
	t.Helper()
	if got := NewRead(nil, state).HasIncomingForeignActionOwners(parent); got != (len(want) != 0) {
		t.Fatalf("incoming action membership=%v want=%v", got, len(want) != 0)
	}
	remaining := make(map[ids.ObjectAddress]*Relation, len(want))
	for _, child := range want { remaining[child.Address()] = child }
	for address, child := range NewRead(nil, state).IncomingForeignActionOwners(parent) {
		if remaining[address] != child { t.Fatalf("unexpected/stale child %v: %p", address, child) }
		delete(remaining, address)
	}
	if len(remaining) != 0 { t.Fatal("missing incoming action owners", remaining) }
}

func TestSnapshotSyncForeignActionIndex(t *testing.T) {
	base, ns, _ := catalogReadFixture()
	parent := catalogTestRelation(9501, ns.Address())
	other := catalogTestRelation(9502, ns.Address())
	child := catalogTestRelation(9503, ns.Address())
	child.Constraints.ForeignKeys = []ForeignKeyConstraint{
		{OID: 9510, Target: parent.Address(), DeleteAction: parser.ForeignKeySetDefault},
		{OID: 9511, Target: parent.Address(), UpdateAction: parser.ForeignKeyCascade},
		{OID: 9512, Target: other.Address(), DeleteAction: parser.ForeignKeySetNull, NotEnforced: true},
	}
	base.SetDescriptor(child.Address(), child)
	snapshotActionOwners(t, base, parent.Address(), child)
	snapshotActionOwners(t, base, other.Address())
	retained := CloneState(base)
	one := *child
	one.ObjectRevision++
	one.Constraints.ForeignKeys = append([]ForeignKeyConstraint(nil), child.Constraints.ForeignKeys[1:]...)
	base.SetDescriptor(one.Address(), &one)
	snapshotActionOwners(t, base, parent.Address(), &one)
	snapshotActionOwners(t, retained, parent.Address(), child)
	// Pointer refresh is required even if target membership did not change:
	// defaults, triggers and CHECKs reside on the new immutable descriptor.
	changed := one
	changed.ObjectRevision++
	changed.Comment = "new recipe owner"
	candidate := NewCandidate(NewRead(nil, base))
	if err := candidate.Replace(&changed); err != nil { t.Fatal(err) }
	next := candidate.State()
	snapshotActionOwners(t, next, parent.Address(), &changed)
	snapshotActionOwners(t, base, parent.Address(), &one)
	moved := changed
	moved.Constraints.ForeignKeys = []ForeignKeyConstraint{{OID: 9511, Target: other.Address(), DeleteAction: parser.ForeignKeyCascade, NotValid: true}}
	next.SetDescriptor(moved.Address(), &moved)
	snapshotActionOwners(t, next, parent.Address())
	snapshotActionOwners(t, next, other.Address(), &moved)
	// Removing a candidate-owned relation must remove the inverse entry;
	// replacing that relation by a nonrelation must do the same.
	removed := NewCandidate(NewRead(nil, next))
	removed.RecordRemoval(ids.ObjectRef{Address: moved.Address()}, &moved)
	snapshotActionOwners(t, removed.State(), other.Address())
	snapshotActionOwners(t, next, other.Address(), &moved)
	nonrelation := *ns
	nonrelation.ObjectAddress = moved.Address()
	replaced := CloneState(next)
	replaced.SetDescriptor(moved.Address(), &nonrelation)
	snapshotActionOwners(t, replaced, other.Address())
	// NO ACTION / RESTRICT are not mutating; NOT VALID still enforces.
	for _, action := range []parser.ForeignKeyAction{parser.ForeignKeyNoAction, parser.ForeignKeyRestrict, parser.ForeignKeyCascade, parser.ForeignKeySetNull, parser.ForeignKeySetDefault} {
		probe := moved
		probe.Constraints.ForeignKeys = []ForeignKeyConstraint{{OID: 9511, Target: other.Address(), DeleteAction: action, NotValid: true}}
		replaced.SetDescriptor(probe.Address(), &probe)
		if MutatingForeignAction(action) {
			snapshotActionOwners(t, replaced, other.Address(), &probe)
		} else {
			snapshotActionOwners(t, replaced, other.Address())
		}
	}
	next.BeginBulkLoad(8)
	next.RemoveDescriptor(moved.Address())
	next.SetDescriptor(child.Address(), child)
	snapshotActionOwners(t, next, parent.Address(), child)
	snapshotActionOwners(t, next, other.Address())
	next.EndBulkLoad()
	snapshotActionOwners(t, next, other.Address())
	snapshotActionOwners(t, next, parent.Address(), child)
	previous := next.ResetMaps()
	snapshotActionOwners(t, next, parent.Address())
	next.BeginBulkLoad(8)
	for address, descriptor := range previous { next.SetDescriptor(address, descriptor) }
	next.EndBulkLoad()
	snapshotActionOwners(t, next, parent.Address(), child)
	next.ResetDescriptors()
	snapshotActionOwners(t, next, parent.Address())
}
