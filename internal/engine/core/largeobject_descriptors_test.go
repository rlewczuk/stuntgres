package core

import (
	"errors"
	"math"
	"testing"
	"unsafe"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/session"
)

func descriptorTestValue(t *testing.T, text string) largeobject.Value {
	t.Helper()
	v, err := (largeobject.Value{}).Write(0, text)
	if err != nil { t.Fatal(err) }
	return v
}

func descriptorTestPage(v largeobject.Value) *byte {
	var first *byte
	v.Chunks(func(offset int64, data []byte) bool {
		if offset == 0 && len(data) != 0 { first = &data[0] }
		return true
	})
	return first
}

func TestLargeObjectDescriptorLifecycle(t *testing.T) {
	content := descriptorTestValue(t, "abcdef")
	table := largeObjectDescriptors{}
	parent, err := table.open(largeObjectDescriptor{oid: 42, content: content})
	if err != nil || parent != 0 { t.Fatal(parent, err) }
	mark := table.ordinal
	child, err := table.open(largeObjectDescriptor{oid: 43, content: content})
	if err != nil || child != 1 { t.Fatal(child, err) }
	if descriptorTestPage(table.slots[parent].content) != descriptorTestPage(content) { t.Fatal("open copied immutable backing") }
	table.slots[parent].offset = 2
	table.rollback(mark)
	if table.slots[parent].offset != 2 || table.slots[child].ordinal != 0 || descriptorTestPage(table.slots[child].content) != nil { t.Fatal("rollback rewound parent or retained child") }
	// Explicit close is not resurrected; reusing its slot acquires a fresh
	// ordinal even when the slot index predates the savepoint.
	table.slots[parent] = largeObjectDescriptor{}
	reused, err := table.open(largeObjectDescriptor{oid: 42, content: content})
	if err != nil || reused != 0 || table.slots[reused].ordinal <= mark { t.Fatal(reused, err) }
	table.rollback(mark)
	if _, err := table.lookup(parent); err == nil { t.Fatal("reused slot escaped rollback") }
	if _, err := table.lookup(-1); err == nil { t.Fatal("negative FD admitted") }
	if _, err := table.lookup(math.MaxInt32); err == nil { t.Fatal("oversize FD admitted") }
	// Released grandchildren have no separate ownership graph: the enclosing
	// ordinal still discards them and repeat rollback never rewinds allocation.
	outer := table.ordinal
	_, _ = table.open(largeObjectDescriptor{oid: 42, content: content})
	_, _ = table.open(largeObjectDescriptor{oid: 43, content: content})
	last := table.ordinal
	table.rollback(outer)
	table.rollback(outer)
	if table.ordinal != last { t.Fatal("rollback rewound ordinal") }
	for _, slot := range table.slots { if slot.ordinal != 0 || descriptorTestPage(slot.content) != nil { t.Fatal("retained aborted backing") } }
	_, _ = table.open(largeObjectDescriptor{oid: 42, content: content})
	_, _ = table.open(largeObjectDescriptor{oid: 43, content: content})
	table.unlink(42)
	if _, err := table.lookup(0); err == nil { t.Fatal("unlink retained matching handle") }
	if _, err := table.lookup(1); err != nil { t.Fatal("unlink closed unrelated handle", err) }
	table.ordinal = math.MaxUint64
	before := table.slots[1]
	if _, err := table.open(largeObjectDescriptor{oid: 44}); err == nil || table.slots[1].ordinal != before.ordinal { t.Fatal("overflow mutated table") }

	// Canonical transaction disposal owns the actual table and stable-base
	// references, including PREPARE transfer and physical-session teardown callers.
	owner := &session.TransactionOwner{Session: &session.Session{Payload: &sessionPayload{}}}
	extension := ownerExtensionOf(owner)
	extension.largeObjects = table
	extension.largeObjectBase = catalog.NewState(1)
	clearTransactionContents(owner)
	if extension.largeObjects.slots != nil || extension.largeObjects.ordinal != 0 || extension.largeObjectBase != nil { t.Fatal("terminal disposal retained descriptor roots") }
}

func TestLargeObjectDescriptorSnapshots(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 42}
	base := catalog.NewState(1)
	old := &catalog.LargeObject{ObjectAddress: a, Content: descriptorTestValue(t, "abcdef")}
	base.SetDescriptor(a, old)
	latest := catalog.CloneState(base)
	peer := *old
	peer.Content = descriptorTestValue(t, "XYcdef")
	latest.SetDescriptor(a, &peer)
	e := &Engine{Catalog: catalog.NewStore(catalog.Owner{}, 16384, 2)}
	e.Catalog.State = latest
	owner := &session.TransactionOwner{Isolation: session.RepeatableRead, Catalog: session.NewJournal()}
	ownerExtensionOf(owner).largeObjectBase = base
	scope := &StatementScope{Owner: owner}
	scope.captureLargeObjectOpeningLocked(e)
	frozen := scope.largeObjectOpening.Get(a).(*catalog.LargeObject)
	if frozen != old || descriptorTestPage(frozen.Content) != descriptorTestPage(old.Content) { t.Fatal("stable opening copied or selected latest committed backing") }
	if e.PrivateCatalogLocked(owner.Catalog).Get(a) != &peer { t.Fatal("WRITE current view lost latest commit") }
	own := *old
	var err error
	own.Content, err = old.Content.Write(2, "UV")
	if err != nil { t.Fatal(err) }
	e.CatalogChangeLocked(owner.Catalog, a, &own)
	if scope.largeObjectOpening.Get(a) != old { t.Fatal("active frontier saw sibling mutation") }
	fresh := &StatementScope{Owner: owner}
	fresh.captureLargeObjectOpeningLocked(e)
	if fresh.largeObjectOpening.Get(a) != &own { t.Fatal("stable base lost own journal overlay") }
	if descriptorTestPage(old.Content) == descriptorTestPage(own.Content) { t.Fatal("write mutated shared page") }
	if data, err := old.Content.Read(0, -1); err != nil || data != "abcdef" { t.Fatal("frozen bytes changed", data, err) }
	data, err := frozen.Content.Read(0, 2)
	if err != nil || data != "ab" || unsafe.StringData(data) == descriptorTestPage(frozen.Content) { t.Fatal("read output aliases immutable page", data, err) }
	// A saved body scratch owns its exact immutable view; neither a nested
	// activation nor an error unwind may replace the outer frontier.
	scratch := scope.sqlRoutineScratch()
	scope.installSQLRoutineScratch(sqlRoutineScratch{largeObjectOpening: fresh.largeObjectOpening})
	if scope.largeObjectOpening.Get(a) != &own { t.Fatal("body frontier missing") }
	scope.installSQLRoutineScratch(scratch)
	if scope.largeObjectOpening.Get(a) != old { t.Fatal("outer frontier not restored") }
	owner.Isolation = session.ReadCommitted
	rc := &StatementScope{Owner: owner, largeObjectRead: catalog.NewRead(&e.Catalog.Owner, latest)}
	rc.captureLargeObjectOpeningLocked(e)
	if rc.largeObjectOpening.Get(a) != &peer { t.Fatal("RC ignored admitted query frontier") }
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead} {
		name := "RC"
		if isolation == session.RepeatableRead { name = "RR" }
		t.Run(name, func(t *testing.T) {
			journal := session.NewJournal()
			owner := &session.TransactionOwner{Isolation: isolation, Catalog: journal}
			ownerExtensionOf(owner).largeObjectBase = base
			outer := catalog.NewRead(&e.Catalog.Owner, base)
			scope := &StatementScope{Owner: owner, largeObjectRead: outer}
			scope.captureLargeObjectOpeningLocked(e)
			opening := scope.largeObjectOpening
			// Argument mutation occurs after root admission. Both body policies
			// borrow this same live journal, but only VOLATILE advances its view.
			e.CatalogChangeLocked(journal, a, &own)
			current := e.PrivateCatalogLocked(journal)
			for _, readOnly := range []bool{true, false} {
				scope.sqlRoutine.policy = routineCommandPolicy{readOnly: readOnly}
				restore := scope.ActivateSQLRoutine(nil, current)
				scope.captureLargeObjectOpeningLocked(e)
				want := &own
				if readOnly { want = old }
				if got := scope.largeObjectOpening.Get(a); got != want { t.Fatal("activation selected wrong immutable frontier", readOnly, got) }
				body := scope.largeObjectOpening
				// Nested nonvolatile activation inherits the body exactly even
				// if its supplied convenience read points elsewhere.
				scope.sqlRoutine.policy = routineCommandPolicy{readOnly: true}
				nested := scope.ActivateSQLRoutine(nil, outer)
				if scope.largeObjectOpening != body { t.Fatal("nested activation recaptured frontier") }
				failure := errors.New("body failure")
				if err := nested(failure); err != failure || scope.largeObjectOpening != body { t.Fatal("nested error restoration", err) }
				if err := restore(nil); err != nil || scope.largeObjectOpening != opening { t.Fatal("successful outer restoration", err) }
				if journal.Descriptors[a] != &own { t.Fatal("activation rewound live journal") }
				restore = scope.ActivateSQLRoutine(nil, current)
				scope.captureLargeObjectOpeningLocked(e)
				if err := restore(failure); err != failure || scope.largeObjectOpening != opening { t.Fatal("failed outer restoration", err) }
			}
			// Without an own overlay, fresh RR still excludes the peer commit;
			// RC uses the newly supplied committed command frontier.
			delete(journal.Descriptors, a)
			scope.sqlRoutine.policy = routineCommandPolicy{}
			restore := scope.ActivateSQLRoutine(nil, catalog.NewRead(&e.Catalog.Owner, latest))
			scope.captureLargeObjectOpeningLocked(e)
			want := &peer
			if isolation == session.RepeatableRead { want = old }
			if scope.largeObjectOpening.Get(a) != want { t.Fatal("fresh body replaced retained committed base") }
			if err := restore(nil); err != nil || scope.largeObjectOpening != opening { t.Fatal("peer-view restoration", err) }
		})
	}
}
