package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func pgLocksUnitCapture(t *testing.T, e *Engine) []lock.Record {
	t.Helper()
	records, err := e.locks.PGLocks(e.catalog.state.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func pgLocksUnitOwnerRecords(records []lock.Record, ticket *lock.Ticket, kind lock.RecordKind) []lock.Record {
	var result []lock.Record
	for _, r := range records {
		if r.Owner.Session == ticket.Ref().Session().ID() && r.Owner.Local == ticket.Ref().ID() && r.Kind == kind {
			result = append(result, r)
		}
	}
	return result
}

func pgLocksUnitAssign(t *testing.T, e *Engine, ticket *lock.Ticket) uint32 {
	t.Helper()
	e.mu.Lock()
	err := e.assignTransactionXIDLocked(ticket.Ref())
	xid := e.visibility.entries[ticket.Ref()].assignedXID
	e.mu.Unlock()
	if err != nil || xid == 0 || ticket.AssignedPublicXID() != xid {
		t.Fatal("canonical assignment mirror", xid, ticket.AssignedPublicXID(), err)
	}
	return xid
}

func TestPGLocksProjectionModes(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgLocksOID})
	if !d.VirtualView || d.RelationKind != catalog.CatalogRelation || d.RowType != 12075 {
		t.Fatal("view identity", d)
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	for _, id := range []ids.OID{12075, 12074} {
		typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.OwningRelation != d.Address() {
			t.Fatal("type owner", typ)
		}
		if err := (typeProvider{}).Validate(view, typ); err != nil {
			t.Fatal(err)
		}
	}
	rowType, _ := view.LookupType(12075)
	array, _ := view.LookupArray(rowType)
	if array.ID() != 12074 || array.Element() != rowType {
		t.Fatal("reciprocal type identity")
	}
	owner := lock.RecordOwner{Session: 7, Local: 9}
	cases := []struct {
		kind lock.RecordKind
		mode lock.Mode
		name string
	}{
		{lock.RelationRecord, lock.AccessShare, "AccessShareLock"}, {lock.RelationRecord, lock.RowShare, "RowShareLock"},
		{lock.RelationRecord, lock.RowExclusive, "RowExclusiveLock"}, {lock.RelationRecord, lock.ShareUpdateExclusive, "ShareUpdateExclusiveLock"},
		{lock.RelationRecord, lock.Share, "ShareLock"}, {lock.RelationRecord, lock.ShareRowExclusive, "ShareRowExclusiveLock"},
		{lock.RelationRecord, lock.Exclusive, "ExclusiveLock"}, {lock.RelationRecord, lock.AccessExclusive, "AccessExclusiveLock"},
		{lock.ObjectRecord, lock.TypeExclusive, "ExclusiveLock"}, {lock.AdvisoryRecord, lock.AdvisoryShare, "ShareLock"}, {lock.AdvisoryRecord, lock.AdvisoryExclusive, "ExclusiveLock"},
		{lock.VirtualRecord, 0, "ExclusiveLock"}, {lock.TransactionRecord, 0, "ExclusiveLock"},
	}
	for _, tc := range cases {
		r := lock.Record{Kind: tc.kind, Owner: owner, Target: owner, Object: 42, Class: 1247, Subobject: 0, XID: 81, Mode: tc.mode}
		rows, err := pgLockRows(context.Background(), d, 17, []lock.Record{r}, nil)
		if err != nil || len(rows) != 1 {
			t.Fatal("projection", tc, rows, err)
		}
		row := rows[0]
		mode, _ := row[12].Text()
		text, _ := row[10].Text()
		pid, _ := row[11].Integer()
		if mode != tc.name || text != "7/9" || pid != 7 || !row[3].IsNull() || !row[4].IsNull() || !row[15].IsNull() {
			t.Fatal("mode/owner/null pattern", tc, row)
		}
		for _, i := range []int{1, 2, 5, 6, 7, 8, 9} {
			present := tc.kind == lock.RelationRecord && (i == 1 || i == 2) || tc.kind == lock.ObjectRecord && (i == 1 || i == 7 || i == 8 || i == 9) || tc.kind == lock.AdvisoryRecord && (i == 1 || i == 7 || i == 8 || i == 9) || tc.kind == lock.VirtualRecord && i == 5 || tc.kind == lock.TransactionRecord && i == 6
			if row[i].IsNull() == present {
				t.Fatal("target NULL matrix", tc, i, row)
			}
		}
	}
	// Both advisory key spaces preserve unsigned halves, never signed OIDs. The
	// lock manager's own tests pin the capture record for each key space.
	for _, subobject := range []int16{1, 2} {
		r := lock.Record{Kind: lock.AdvisoryRecord, Owner: owner, Mode: lock.AdvisoryShare | lock.AdvisoryExclusive, Class: math.MaxUint32, Object: 1 << 31, Subobject: subobject}
		rows, err := pgLockRows(context.Background(), d, 17, []lock.Record{r}, nil)
		if err != nil || len(rows) != 2 {
			t.Fatal("held mode expansion", rows, err)
		}
		for _, row := range rows {
			high, _ := row[7].OID()
			low, _ := row[8].OID()
			if high != math.MaxUint32 || low != 1<<31 {
				t.Fatal("unsigned identity", row)
			}
		}
	}
	if rows, err := pgLockRows(context.Background(), d, 17, []lock.Record{{Kind: lock.RelationRecord, Owner: owner}}, nil); err != nil || len(rows) != 0 {
		t.Fatal("zero-mode phantom", rows, err)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	pending := lock.Record{Kind: lock.TransactionRecord, Owner: owner, XID: 81, Waiting: true, WaitStart: stamp}
	rows, err := pgLockRows(context.Background(), d, 17, []lock.Record{pending}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := rows[0][15].TimestampTime()
	mode, _ := rows[0][12].Text()
	granted, _ := rows[0][13].Boolean()
	if !got.Equal(stamp) || mode != "ShareLock" || granted {
		t.Fatal("pending timestamp/mode", rows)
	}
}

func TestPGLocksTransactionLifetime(t *testing.T) {
	t.Run("registration_assignment_restore_prepare_end", func(t *testing.T) {
		e := New()
		_, ticket := blockingUnitOwner(t, e)
		if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, lock.VirtualRecord)) != 1 || ticket.AssignedPublicXID() != 0 {
			t.Fatal("register did not own exact VXID")
		}
		xid := pgLocksUnitAssign(t, e, ticket)
		held := e.locks.Checkpoint(ticket)
		e.locks.Restore(ticket, held)
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.Ref())
		e.dropVisibilitySnapshotLocked(ticket.Ref())
		e.mu.Unlock()
		if ticket.AssignedPublicXID() != xid {
			t.Fatal("history/snapshot consumed lock ownership")
		}
		// Final publication/history consumption is not the manager's release
		// boundary. In particular, a detached completion wait must retain its
		// real XID through the publication-to-manager-end interval.
		xid = pgLocksUnitAssign(t, e, ticket)
		e.locks.DetachPrepared(ticket)
		_, waiter := blockingUnitOwner(t, e)
		request, _ := pgLocksUnitRequest(t, e, waiter, lock.CompletionOf(ticket), lock.TransactionCompletion, lock.CompletionVirtual)
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.Ref())
		e.mu.Unlock()
		pending := pgLocksUnitWait(t, e, waiter)
		if pending.Kind != lock.TransactionRecord || pending.XID != xid || request == nil || request.Terminal() {
			t.Fatal("prepared publication gap fabricated VXID or lost dependency", pending)
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, ticket, lock.VirtualRecord)) != 0 {
			t.Fatal("prepared owns live VXID")
		}
		xids := pgLocksUnitOwnerRecords(records, ticket, lock.TransactionRecord)
		if len(xids) != 1 || xids[0].XID != xid || !xids[0].Owner.Prepared {
			t.Fatal("prepared identity", xids)
		}
		e.endLockOwner(ticket)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
		if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, lock.TransactionRecord)) != 0 {
			t.Fatal("ended transaction owns XID")
		}
	})
	t.Run("visibility_association_preserves_values_and_exhaustion", func(t *testing.T) {
		e := New()
		_, ticket := blockingUnitOwner(t, e)
		v := e.visibility.entries[ticket.Ref()]
		if v.ticket != ticket {
			t.Fatal("registration lost assignment association")
		}
		e.mu.Lock()
		_, err := e.visibilitySnapshotLocked(ticket.Ref(), false)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		before := e.visibility.entries[ticket.Ref()]
		e.visibility.assignedXID = math.MaxUint64
		e.mu.Lock()
		err = e.assignTransactionXIDLocked(ticket.Ref())
		e.mu.Unlock()
		if err == nil || e.visibility.entries[ticket.Ref()] != before || ticket.AssignedPublicXID() != 0 {
			t.Fatal("exhaustion partially mutated", err)
		}
		e.visibility.assignedXID = 2
		pgLocksUnitAssign(t, e, ticket)
		if e.visibility.entries[ticket.Ref()].xmin != before.xmin || !e.visibility.entries[ticket.Ref()].snapshot {
			t.Fatal("assignment discarded visibility")
		}
		e.locks.Fence(ticket.Ref().Epoch() + 1)
		if rows, err := e.locks.PGLocks(ticket.Ref().Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) || rows != nil {
			t.Fatal("old epoch snapshot", rows, err)
		}
		if rows, err := e.locks.PGLocks(ticket.Ref().Epoch() + 1); err != nil || len(rows) != 0 {
			t.Fatal("fenced owner resurrection", rows, err)
		}
	})
	t.Run("phase_reset_retains_assignment_association", func(t *testing.T) {
		e := New()
		_, ticket := blockingUnitOwner(t, e)
		e.mu.Lock()
		v := e.visibility.entries[ticket.Ref()]
		v.xid, v.xmin, v.snapshot, v.safeBuilder = 91, 13, true, true
		e.visibility.entries[ticket.Ref()] = v
		e.resetVisibilityPhaseLocked(ticket.Ref())
		after := e.visibility.entries[ticket.Ref()]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket}) {
			t.Fatal("phase reset lost ticket or retained advertisement", after)
		}
		xid := pgLocksUnitAssign(t, e, ticket)
		e.mu.Lock()
		v = e.visibility.entries[ticket.Ref()]
		v.xid, v.xmin, v.snapshot, v.safeBuilder = 92, 14, true, true
		e.visibility.entries[ticket.Ref()] = v
		e.resetVisibilityPhaseLocked(ticket.Ref())
		after = e.visibility.entries[ticket.Ref()]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket, assignedXID: xid, fullXID: uint64(xid)}) || ticket.AssignedPublicXID() != xid {
			t.Fatal("phase reset discarded real assigned ownership", after)
		}
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.Ref())
		if ticket.AssignedPublicXID() != xid {
			e.mu.Unlock()
			t.Fatal("bookkeeping impersonated manager release")
		}
		e.resetVisibilityPhaseLocked(ticket.Ref())
		after = e.visibility.entries[ticket.Ref()]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket}) || ticket.AssignedPublicXID() != 0 {
			t.Fatal("completed phase retained XID or lost association", after)
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, ticket, lock.TransactionRecord)) != 0 || len(pgLocksUnitOwnerRecords(records, ticket, lock.VirtualRecord)) != 1 {
			t.Fatal("phase retirement changed VXID or retained completed XID", records)
		}
		next := pgLocksUnitAssign(t, e, ticket)
		if next <= xid {
			t.Fatal("later phase reused completed XID", xid, next)
		}
	})
	t.Run("public_backend_boundary", func(t *testing.T) {
		e := New()
		ref := e.catalog.Domain.Tx(e.catalog.Domain.Session(math.MaxInt32+1), 1, 0, nil, 0)
		if _, _, err := lock.PublicOwner(e.catalog.Domain, ref, false); err == nil || err.Error() != "stuntgres: lock session identity exceeds integer range" {
			t.Fatal("PID overflow", err)
		}
		if owner, ok, err := lock.PublicOwner(e.catalog.Domain, ref, true); err != nil || !ok || !owner.Prepared {
			t.Fatal("prepared PID must be NULL", owner, ok, err)
		}
		ref = withTxSession(ref, e.catalog.Domain.PrivateSession(&Session{}))
		if _, ok, err := lock.PublicOwner(e.catalog.Domain, ref, false); err != nil || ok {
			t.Fatal("private maintenance backend leaked", ok, err)
		}
	})
	for _, route := range []string{"insert", "row_grant", "catalog", "prepare"} {
		t.Run("assignment_"+route, func(t *testing.T) { pgLocksUnitAssignmentRoute(t, route) })
	}
}

// Exercise actual package staging/grant/publication entrypoints, without SQL or
// a second public-XID allocator. The provider fixture supplies real catalog roots.
func pgLocksUnitAssignmentRoute(t *testing.T, route string) {
	t.Helper()
	e, _, d := typedUnitCatalog(t, false)
	session, ticket := blockingUnitOwner(t, e)
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	scope.owner.mu.Lock()
	scope.attachTransactionLocked()
	scope.owner.mu.Unlock()
	scope.worked = true
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
	store := e.storage.Relations[d.Address()]
	scope.lockedRef, scope.lockedStorage, scope.lockedDescriptor = ref, store, d
	before := e.visibility.assignedXID
	switch route {
	case "insert":
		v, _ := sqltype.Integer(testUsage(e, sqltype.Int4).Type, 1)
		err = scope.Writes().Apply(ref, [][]sqltype.Value{{v, sqltype.String(testUsage(e, sqltype.Text).Type, "owned")}})
	case "row_grant":
		_, err = scope.acquireQueryRow(queryLeaf{ref: ref, relation: d, storage: store}, 1, queryRowMark{mode: lock.ForUpdate, wait: parser.RowLockBlock})
	case "catalog":
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		next := *d
		next.ObjectRevision++
		if err = stage.Replace(&next); err == nil {
			var publication *PreparedCatalogPublication
			publication, err = e.ValidateCatalogStage(context.Background(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(op, scope, publication, PublicationToken{Epoch: ref.Epoch})
			}
		}
	case "prepare":
		e.prepared = make(map[string]*preparedTransaction)
		entry := &preparedTransaction{ref: ticket.Ref(), ticket: ticket, gid: "pg_locks_unit_prepare"}
		err = e.publishRowsPrepared(op, ticket.Ref(), nil, nil, nil, nil, &preparedPublication{entry: entry, preparing: true, origin: scope.owner}, scope)
		if err == nil {
			e.locks.DetachPrepared(ticket)
		}
	}
	if err != nil {
		t.Fatal(route, err)
	}
	if uint64(ticket.AssignedPublicXID()) != before+1 || e.visibility.assignedXID != before+1 {
		t.Fatal("actual route failed to mirror exactly one canonical assignment", route, before, ticket.AssignedPublicXID(), e.visibility.assignedXID)
	}
	if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, lock.TransactionRecord)) != 1 {
		t.Fatal("actual assignment lacks completion ownership", route)
	}
	if route == "prepare" {
		delete(e.prepared, "pg_locks_unit_prepare")
		e.endLockOwner(ticket)
	}
}

func TestPGLocksPredicateLifetime(t *testing.T) {
	e, _, d := typedUnitCatalog(t, false)
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enrollSerializableLocked(a.Ref(), Serializable)
	e.enrollSerializableLocked(b.Ref(), Serializable)
	first, overlap := e.visibility.serializable.nodes[a.Ref()], e.visibility.serializable.nodes[b.Ref()]
	first.captured, overlap.captured = true, true
	first.reads[d.Address()] = struct{}{}
	before, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(before) != 1 {
		t.Fatal("real SSI read", before, err)
	}
	first.committed = true
	e.endSerializableLocked(a.Ref(), true)
	after, err := e.capturePGPredicateLocksLocked()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("committed overlap changed original owner", before, after, err)
	}
	first.prepared = true
	prepared, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(prepared) != 1 || !prepared[0].Owner.Prepared {
		t.Fatal("prepared SSI association", prepared, err)
	}
	first.aborted = true
	aborted, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(aborted) != 0 {
		t.Fatal("aborted predicate leaked", aborted, err)
	}
	e.endSerializableLocked(b.Ref(), false)
	if records, err := e.capturePGPredicateLocksLocked(); err != nil || len(records) != 0 {
		t.Fatal("overlap cleanup", records, err)
	}
	e.fenceSerializableLocked()
}
