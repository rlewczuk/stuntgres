package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/util"
)

func TestConstraintPendingEventOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
	sessionSQL(t, s, "INSERT INTO p VALUES(1)")
	transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
	ownerModes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if len(ownerModes.Events) != 1 || ownerModes.Events[0].ForeignKey == nil {
		t.Fatal("missing actual owner event")
	}
	child, parent := ownerModes.Events[0].Parent, ownerModes.Events[0].ForeignKey.Parent
	scope := &core.StatementScope{Owner: s.Transactions}
	if !scope.HasPendingConstraintEvents(child.Address) || scope.HasPendingConstraintEvents(parent.Address) {
		t.Fatal("owner query confused FK endpoints")
	}
	payload := ownerModes.Events[0].ForeignKey
	// Started scope state is authoritative even when it consumed all owner events.
	core.SetScopeIndexesSlot(scope, &index.Scope{ConstraintsStarted: true})
	if scope.HasPendingConstraintEvents(child.Address) {
		t.Fatal("consumed scope event resurrected from owner")
	}
	// An unstarted scratch slot does not shadow the transaction's events.
	core.SetScopeIndexesSlot(scope, &index.Scope{})
	if !scope.HasPendingConstraintEvents(child.Address) {
		t.Fatal("scratch slot hid owner event")
	}
	other := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 29999}}
	modes := index.ConstraintModes{AllSet: true, AllDeferred: true, ByConstraint: map[ids.OID]bool{42: false}, Events: []index.ConstraintEvent{
		{Parent: other, Constraint: 42, Row: 999, Version: 888},
		{Parent: parent, Constraint: 43, ForeignKey: payload},
	}}
	before := append([]index.ConstraintEvent(nil), modes.Events...)
	slot := &index.Scope{ConstraintsStarted: true, ConstraintModes: modes}
	core.SetScopeIndexesSlot(scope, slot)
	// No row/storage lookup is required: even a superseded incarnation remains
	// a queued event until the completion authority explicitly consumes it.
	if !scope.HasPendingConstraintEvents(other.Address) || !scope.HasPendingConstraintEvents(parent.Address) || scope.HasPendingConstraintEvents(child.Address) {
		t.Fatal("scope query filtered events or used FK payload endpoints")
	}
	if &slot.ConstraintModes.Events[0] != &modes.Events[0] || !reflect.DeepEqual(slot.ConstraintModes.Events, before) || !slot.ConstraintModes.AllSet || !slot.ConstraintModes.AllDeferred || slot.ConstraintModes.ByConstraint[42] {
		t.Fatal("query mutated effective state")
	}
	after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if !reflect.DeepEqual(ownerModes, after) || &ownerModes.Events[0] != &after.Events[0] || after.Events[0].ForeignKey != payload {
		t.Fatal("query changed owner payload/backing")
	}
}

func TestConstraintPendingEventZeroAllocation(t *testing.T) {
	owner := &session.TransactionOwner{}
	scope := &core.StatementScope{Owner: owner}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 29999}
	var found bool
	allocations, bytes := util.AllocationsPerCall(100, func() { found = scope.HasPendingConstraintEvents(address) })
	if found || allocations != 0 || bytes != 0 || owner.Extension != nil {
		t.Fatal("empty query allocated state", found, allocations, bytes)
	}
	core.SetScopeIndexesSlot(scope, &index.Scope{ConstraintsStarted: true})
	allocations, bytes = util.AllocationsPerCall(100, func() { found = scope.HasPendingConstraintEvents(address) })
	if found || allocations != 0 || bytes != 0 || owner.Extension != nil {
		t.Fatal("settled scope query allocated state", found, allocations, bytes)
	}
}
