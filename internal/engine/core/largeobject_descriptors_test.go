package core

import (
	"errors"
	"math"
	"sync"
	"testing"
	"unsafe"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
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

func TestLargeObjectDescriptorTruncate(t *testing.T) {
	a := ids.ObjectAddress{ClassID:ids.LargeObjectClassID, ObjectID:42}
	old := &catalog.LargeObject{ObjectAddress:a, ObjectRevision:1, Content:descriptorTestValue(t, "abcdef")}
	e := &Engine{Mu:new(sync.Mutex), Catalog:catalog.NewStore(catalog.Owner{}, 16384, 2)}
	e.Catalog.State = catalog.NewState(1)
	e.Catalog.State.SetDescriptor(a, old)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	owner := &session.TransactionOwner{Catalog:session.NewJournal()}
	backend := &session.Session{Transactions:owner}
	op := &session.Operation{Session:backend}
	scope := &StatementScope{Op:op, Owner:owner}
	table := &ownerExtensionOf(owner).largeObjects
	read, err := table.open(largeObjectDescriptor{oid:42, content:old.Content})
	if err != nil { t.Fatal(err) }
	write, err := table.open(largeObjectDescriptor{oid:42, writable:true, offset:1<<33})
	if err != nil { t.Fatal(err) }
	mark, ordinal := owner.Catalog, table.ordinal
	call := func(name string, fd int32, length int64) (functions.LargeObjectResult, error) {
		return scope.largeObjectDescriptorLocked(e, functions.LargeObjectRequest{Name:name, Descriptor:fd, Length:length})
	}
	assertFailure := func(name string, fd int32, length int64, code, message string) {
		t.Helper()
		candidate, revision, cursor := scope.Catalog, e.Catalog.NextRevision(), table.slots[write].offset
		out, err := call(name, fd, length)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code || d.Message != message { t.Fatal("truncate diagnostic", err) }
		if out != (functions.LargeObjectResult{}) || scope.Catalog != candidate || e.Catalog.NextRevision() != revision || table.slots[write].offset != cursor || e.Catalog.State.Get(a) != old { t.Fatal("failure published or moved cursor") }
	}
	for _, name := range []string{"lo_truncate", "lo_truncate64"} {
		owner.ReadOnly = true
		assertFailure(name, -1, -1, "25006", "cannot execute "+name+"() in a read-only transaction")
		owner.ReadOnly = false
		assertFailure(name, -1, -1, "42704", "invalid large-object descriptor: -1")
		assertFailure(name, read, -1, "55000", "large object descriptor 0 was not opened for writing")
		assertFailure(name, write, -1, "22023", "invalid large object truncation target: -1")
		out, err := call(name, write, 2)
		if err != nil || out.Integer != 0 { t.Fatal("truncate result", out, err) }
		changed, ok := scope.Catalog.Descriptors[a].(*catalog.LargeObject)
		if !ok || changed == old || changed.Revision() <= old.Revision() || descriptorTestPage(changed.Content) == descriptorTestPage(old.Content) { t.Fatal("replacement did not own new descriptor/endpoint/revision") }
		if scope.Catalog == mark || len(mark.Descriptors) != 0 || scope.Catalog.Originals[a] != old || e.Catalog.State.Get(a) != old { t.Fatal("candidate mutated owner journal or committed catalog") }
		if got, err := changed.Content.Read(0, -1); err != nil || got != "ab" { t.Fatal("candidate bytes", got, err) }
		if got, err := table.slots[read].content.Read(0, -1); err != nil || got != "abcdef" || descriptorTestPage(table.slots[read].content) != descriptorTestPage(old.Content) { t.Fatal("frozen opening backing changed", got, err) }
		if table.slots[write].offset != 1<<33 { t.Fatal("truncate narrowed or moved int64 cursor") }
		// Savepoint marks retain journals and opening ordinals, not cursors.
		table.slots[write].offset++
		scope.Catalog = nil
		owner.Catalog = mark
		table.rollback(ordinal)
		if e.PrivateCatalogLocked(scope.PendingCatalog()).Get(a) != old || table.slots[write].offset != (1<<33)+1 { t.Fatal("rollback lost old descriptor or rewound cursor") }
		table.slots[write].offset = 1<<33
	}
	out, err := call("lo_truncate64", write, largeobject.MaximumSize)
	if err != nil || out.Integer != 0 || table.slots[write].offset != 1<<33 { t.Fatal("large truncate changed cursor", out, err) }
	huge := scope.Catalog.Descriptors[a].(*catalog.LargeObject)
	chunks := 0
	huge.Content.Chunks(func(offset int64, data []byte) bool {
		chunks++
		if offset == largeobject.MaximumSize && len(data) != 0 { t.Fatal("maximum endpoint nonempty") }
		return true
	})
	if chunks != 2 || descriptorTestPage(huge.Content) != descriptorTestPage(old.Content) { t.Fatal("extension copied lower payload or materialized gap") }
	// A failed statement discards its candidate without publishing it.
	scope.Catalog = nil
	if e.PrivateCatalogLocked(scope.PendingCatalog()).Get(a) != old || e.Catalog.State.Get(a) != old { t.Fatal("failed statement retained private bytes") }
	e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(math.MaxUint64))
	if _, err := call("lo_truncate64", write, 1); err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" || scope.Catalog != nil || table.slots[write].offset != 1<<33 || len(mark.Descriptors) != 0 { t.Fatal("failed revision reservation published", err) }
	// Reject a foreign transaction owner before changing any resource.
	scope.TxRef = e.Catalog.Domain.Tx(ids.SessionToken{}, 1, 1, nil, 0)
	if _, err := call("lo_truncate64", write, 1); !errors.Is(err, ids.ErrCatalogReplaced) || scope.Catalog != nil || table.slots[write].offset != 1<<33 { t.Fatal("foreign owner changed descriptor", err) }
}

func TestLargeObjectSeekDescriptorState(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 42}
	old := &catalog.LargeObject{ObjectAddress: a, ObjectRevision: 1, Content: descriptorTestValue(t, "abcdef")}
	e := &Engine{Mu: new(sync.Mutex), Catalog: catalog.NewStore(catalog.Owner{}, 16384, 2)}
	e.Catalog.State = catalog.NewState(1)
	e.Catalog.State.SetDescriptor(a, old)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	owner := &session.TransactionOwner{Catalog: session.NewJournal()}
	backend := &session.Session{Transactions: owner}
	scope := &StatementScope{Op: &session.Operation{Session: backend}, Owner: owner}
	table := &ownerExtensionOf(owner).largeObjects
	read, err := table.open(largeObjectDescriptor{oid: 42, content: old.Content})
	if err != nil { t.Fatal(err) }
	write, err := table.open(largeObjectDescriptor{oid: 42, writable: true})
	if err != nil { t.Fatal(err) }
	journal, revision, page := owner.Catalog, e.Catalog.NextRevision(), descriptorTestPage(old.Content)
	call := func(name string, fd int32, offset int64, whence int32) (functions.LargeObjectResult, error) {
		return scope.largeObjectDescriptorLocked(e, functions.LargeObjectRequest{Name: name, Descriptor: fd, Offset: offset, Whence: whence})
	}
	assertUnpublished := func() {
		t.Helper()
		if scope.Catalog != nil || owner.Catalog != journal || len(journal.Descriptors) != 0 || e.Catalog.NextRevision() != revision || descriptorTestPage(old.Content) != page || old.Content.Size() != 6 { t.Fatal("cursor operation published or changed content") }
	}
	position := func(name string, fd int32, offset int64, whence int32, want int64) {
		t.Helper()
		out, err := call(name, fd, offset, whence)
		if err != nil || out.Position != want || table.slots[fd].offset != want || out.Integer != 0 { t.Fatal("position", name, out, want, err) }
		assertUnpublished()
	}
	failure := func(name string, fd int32, offset int64, whence int32, code, message string, want int64) {
		t.Helper()
		out, err := call(name, fd, offset, whence)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code || d.Message != message || out != (functions.LargeObjectResult{}) { t.Fatal("seek diagnostic", name, out, err) }
		if fd >= 0 && table.slots[fd].offset != want { t.Fatal("failure cursor", table.slots[fd].offset, want) }
		assertUnpublished()
	}
	for _, fd := range []int32{read, write} {
		owner.ReadOnly = true
		position("lo_tell", fd, 0, 0, 0)
		position("lo_lseek", fd, 2, 0, 2)
		position("lo_lseek64", fd, 1, 1, 3)
		position("lo_lseek64", fd, -1, 2, 5)
		position("lo_tell64", fd, 0, 0, 5)
		owner.ReadOnly = false
		position("lo_lseek64", fd, 3, 0, 3)
		failure("lo_lseek64", fd, -1, 3, "22023", "invalid whence setting: 3", 3)
		failure("lo_lseek64", fd, -4, 1, "22023", "invalid large object seek target: -1", 3)
		failure("lo_lseek64", fd, -7, 2, "22023", "invalid large object seek target: -1", 3)
		failure("lo_lseek64", fd, largeobject.MaximumSize+1, 0, "22023", "invalid large object seek target: 4398046509057", 3)
		position("lo_lseek64", fd, 1, 0, 1)
		failure("lo_lseek64", fd, math.MaxInt64, 1, "22023", "invalid large object seek target: -9223372036854775808", 1)
		failure("lo_lseek64", fd, math.MaxInt64, 2, "22023", "invalid large object seek target: -9223372036854775803", 1)
		position("lo_lseek", fd, math.MaxInt32, 0, math.MaxInt32)
		message := "lo_lseek result out of range for large-object descriptor 0"
		tellMessage := "lo_tell result out of range for large-object descriptor 0"
		if fd == write {
			message = "lo_lseek result out of range for large-object descriptor 1"
			tellMessage = "lo_tell result out of range for large-object descriptor 1"
		}
		mark := table.ordinal
		child, err := table.open(largeObjectDescriptor{oid: 42, content: old.Content})
		if err != nil { t.Fatal(err) }
		failure("lo_lseek", fd, 1, 1, "22003", message, int64(math.MaxInt32)+1)
		table.rollback(mark)
		if _, err := table.lookup(child); err == nil || descriptorTestPage(table.slots[child].content) != nil { t.Fatal("savepoint retained child backing", err) }
		position("lo_tell64", fd, 0, 0, int64(math.MaxInt32)+1)
		failure("lo_tell", fd, 0, 0, "22003", tellMessage, int64(math.MaxInt32)+1)
		position("lo_lseek", fd, -1, 1, math.MaxInt32)
		position("lo_lseek64", fd, largeobject.MaximumSize, 0, largeobject.MaximumSize)
	}
	failure("lo_lseek64", -1, -1, -1, "42704", "invalid large-object descriptor: -1", 0)
	// END selects current private bytes for WRITE and immutable opening bytes
	// for READ, without cloning either descriptor or sparse page backing.
	peer := *old
	peer.Content = descriptorTestValue(t, "abcdefgh")
	e.Catalog.State = catalog.CloneState(e.Catalog.State)
	e.Catalog.State.SetDescriptor(a, &peer)
	position("lo_lseek64", read, 0, 2, 6)
	position("lo_lseek64", write, 0, 2, 8)
	own := peer
	own.Content = descriptorTestValue(t, "abc")
	e.CatalogChangeLocked(journal, a, &own)
	out, err := call("lo_lseek64", write, 0, 2)
	if err != nil || out.Position != 3 || scope.Catalog != nil || journal.Descriptors[a] != &own || e.Catalog.NextRevision() != revision { t.Fatal("END ignored or published own journal", out, err) }
	out, err = call("lo_lseek64", read, 0, 2)
	if err != nil || out.Position != 6 || descriptorTestPage(table.slots[read].content) != page { t.Fatal("END changed frozen backing", out, err) }
	delete(journal.Descriptors, a)
	e.Catalog.State = catalog.CloneState(e.Catalog.State)
	e.Catalog.State.RemoveDescriptor(a)
	// No live object remains. SET/CUR/tell still use the actual owner table;
	// only END resolves content, selecting empty current versus frozen bytes.
	for _, fd := range []int32{read, write} {
		position("lo_lseek64", fd, 2, 0, 2)
		position("lo_lseek", fd, 1, 1, 3)
		position("lo_tell", fd, 0, 0, 3)
		position("lo_tell64", fd, 0, 0, 3)
	}
	position("lo_lseek64", read, 0, 2, 6)
	position("lo_lseek64", write, 0, 2, 0)
	// The owner identity fence precedes lookup and leaves the retained table.
	scope.TxRef = e.Catalog.Domain.Tx(ids.SessionToken{}, 1, 1, nil, 0)
	if _, err := call("lo_tell64", read, 0, 0); !errors.Is(err, ids.ErrCatalogReplaced) || table.slots[read].offset != 6 { t.Fatal("foreign owner changed cursor", err) }
}
