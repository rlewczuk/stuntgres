package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func pgLocksUnitCapture(t *testing.T, e *Engine) []pgLockRecord {
	t.Helper()
	records, err := e.locks.snapshotPGLocks(e.state.epoch)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func pgLocksUnitOwnerRecords(records []pgLockRecord, ticket *relationTicket, kind pgLockKind) []pgLockRecord {
	var result []pgLockRecord
	for _, r := range records {
		if r.owner.session == ticket.ref.session.id && r.owner.local == ticket.ref.id && r.kind == kind {
			result = append(result, r)
		}
	}
	return result
}

func pgLocksUnitAssign(t *testing.T, e *Engine, ticket *relationTicket) uint32 {
	t.Helper()
	e.mu.Lock()
	err := e.assignTransactionXIDLocked(ticket.ref)
	xid := e.visibility[ticket.ref].assignedXID
	e.mu.Unlock()
	if err != nil || xid == 0 || ticket.assignedPublicXID() != xid {
		t.Fatal("canonical assignment mirror", xid, ticket.assignedPublicXID(), err)
	}
	return xid
}

func TestPGLocksProjectionModes(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: pgLocksOID}].(*relationDescriptor)
	if !d.virtualView || d.relationKind != catalogRelation || d.rowType != 12075 {
		t.Fatal("view identity", d)
	}
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	for _, id := range []OID{12075, 12074} {
		typ := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: id}].(*typeDescriptor)
		if typ.owningRelation != d.address {
			t.Fatal("type owner", typ)
		}
		if err := (typeProvider{}).Validate(catalog, typ); err != nil {
			t.Fatal(err)
		}
	}
	rowType, _ := catalog.LookupType(12075)
	array, _ := catalog.LookupArray(rowType)
	if array.ID() != 12074 || array.Element() != rowType {
		t.Fatal("reciprocal type identity")
	}
	owner := pgLockOwner{session: 7, local: 9}
	cases := []struct {
		kind pgLockKind
		mode relationLockMode
		name string
	}{
		{pgRelationLock, accessShare, "AccessShareLock"}, {pgRelationLock, relationRowShare, "RowShareLock"},
		{pgRelationLock, rowExclusive, "RowExclusiveLock"}, {pgRelationLock, shareUpdateExclusive, "ShareUpdateExclusiveLock"},
		{pgRelationLock, relationShare, "ShareLock"}, {pgRelationLock, shareRowExclusive, "ShareRowExclusiveLock"},
		{pgRelationLock, relationExclusive, "ExclusiveLock"}, {pgRelationLock, accessExclusive, "AccessExclusiveLock"},
		{pgObjectLock, typeExclusive, "ExclusiveLock"}, {pgAdvisoryLock, advisoryShared, "ShareLock"}, {pgAdvisoryLock, advisoryExclusive, "ExclusiveLock"},
		{pgVirtualLock, 0, "ExclusiveLock"}, {pgTransactionLock, 0, "ExclusiveLock"},
	}
	for _, tc := range cases {
		r := pgLockRecord{kind: tc.kind, owner: owner, target: owner, object: 42, class: 1247, subobject: 0, xid: 81, mode: tc.mode}
		rows, err := pgLockRows(context.Background(), d, 17, []pgLockRecord{r}, nil)
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
			present := tc.kind == pgRelationLock && (i == 1 || i == 2) || tc.kind == pgObjectLock && (i == 1 || i == 7 || i == 8 || i == 9) || tc.kind == pgAdvisoryLock && (i == 1 || i == 7 || i == 8 || i == 9) || tc.kind == pgVirtualLock && i == 5 || tc.kind == pgTransactionLock && i == 6
			if row[i].IsNull() == present {
				t.Fatal("target NULL matrix", tc, i, row)
			}
		}
	}
	// Both advisory key spaces preserve unsigned halves, never signed OIDs.
	for _, kind := range []lockResourceKind{advisoryBigintResource, advisoryPairResource} {
		r, err := pgResourceRecord(lockResource{kind: kind, high: math.MaxUint32, low: 1 << 31}, owner, advisoryShared|advisoryExclusive)
		if err != nil || r.subobject != int16(kind) {
			t.Fatal("advisory key space", r, err)
		}
		rows, err := pgLockRows(context.Background(), d, 17, []pgLockRecord{r}, nil)
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
	if rows, err := pgLockRows(context.Background(), d, 17, []pgLockRecord{{kind: pgRelationLock, owner: owner}}, nil); err != nil || len(rows) != 0 {
		t.Fatal("zero-mode phantom", rows, err)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	pending := pgLockRecord{kind: pgTransactionLock, owner: owner, xid: 81, waiting: true, waitstart: stamp}
	rows, err := pgLockRows(context.Background(), d, 17, []pgLockRecord{pending}, nil)
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
		if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, pgVirtualLock)) != 1 || ticket.assignedPublicXID() != 0 {
			t.Fatal("register did not own exact VXID")
		}
		xid := pgLocksUnitAssign(t, e, ticket)
		held := e.locks.checkpoint(ticket)
		e.locks.restore(ticket, held)
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.ref)
		e.dropVisibilitySnapshotLocked(ticket.ref)
		e.mu.Unlock()
		if ticket.assignedPublicXID() != xid {
			t.Fatal("history/snapshot consumed lock ownership")
		}
		// Final publication/history consumption is not the manager's release
		// boundary. In particular, a detached completion wait must retain its
		// real XID through the publication-to-manager-end interval.
		xid = pgLocksUnitAssign(t, e, ticket)
		e.locks.detachPrepared(ticket)
		_, waiter := blockingUnitOwner(t, e)
		request, _ := pgLocksUnitRequest(t, e, waiter, lockResource{completion: ticket}, transactionCompletion, completionVirtual)
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.ref)
		e.mu.Unlock()
		pending := pgLocksUnitWait(t, e, waiter)
		if pending.kind != pgTransactionLock || pending.xid != xid || request == nil || request.terminal {
			t.Fatal("prepared publication gap fabricated VXID or lost dependency", pending)
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, ticket, pgVirtualLock)) != 0 {
			t.Fatal("prepared owns live VXID")
		}
		xids := pgLocksUnitOwnerRecords(records, ticket, pgTransactionLock)
		if len(xids) != 1 || xids[0].xid != xid || !xids[0].owner.prepared {
			t.Fatal("prepared identity", xids)
		}
		e.locks.end(ticket)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
		if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, pgTransactionLock)) != 0 {
			t.Fatal("ended transaction owns XID")
		}
	})
	t.Run("visibility_association_preserves_values_and_exhaustion", func(t *testing.T) {
		e := New()
		_, ticket := blockingUnitOwner(t, e)
		v := e.visibility[ticket.ref]
		if v.ticket != ticket {
			t.Fatal("registration lost assignment association")
		}
		e.mu.Lock()
		_, err := e.visibilitySnapshotLocked(ticket.ref, false)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		before := e.visibility[ticket.ref]
		e.assignedXID = math.MaxUint64
		e.mu.Lock()
		err = e.assignTransactionXIDLocked(ticket.ref)
		e.mu.Unlock()
		if err == nil || e.visibility[ticket.ref] != before || ticket.assignedPublicXID() != 0 {
			t.Fatal("exhaustion partially mutated", err)
		}
		e.assignedXID = 2
		pgLocksUnitAssign(t, e, ticket)
		if e.visibility[ticket.ref].xmin != before.xmin || !e.visibility[ticket.ref].snapshot {
			t.Fatal("assignment discarded visibility")
		}
		e.locks.fence(ticket.ref.epoch + 1)
		if rows, err := e.locks.snapshotPGLocks(ticket.ref.epoch); !errors.Is(err, ErrCatalogReplaced) || rows != nil {
			t.Fatal("old epoch snapshot", rows, err)
		}
		if rows, err := e.locks.snapshotPGLocks(ticket.ref.epoch + 1); err != nil || len(rows) != 0 {
			t.Fatal("fenced owner resurrection", rows, err)
		}
	})
	t.Run("phase_reset_retains_assignment_association", func(t *testing.T) {
		e := New()
		_, ticket := blockingUnitOwner(t, e)
		e.mu.Lock()
		v := e.visibility[ticket.ref]
		v.xid, v.xmin, v.snapshot, v.safeBuilder = 91, 13, true, true
		e.visibility[ticket.ref] = v
		e.resetVisibilityPhaseLocked(ticket.ref)
		after := e.visibility[ticket.ref]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket}) {
			t.Fatal("phase reset lost ticket or retained advertisement", after)
		}
		xid := pgLocksUnitAssign(t, e, ticket)
		e.mu.Lock()
		v = e.visibility[ticket.ref]
		v.xid, v.xmin, v.snapshot, v.safeBuilder = 92, 14, true, true
		e.visibility[ticket.ref] = v
		e.resetVisibilityPhaseLocked(ticket.ref)
		after = e.visibility[ticket.ref]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket, assignedXID: xid, fullXID: uint64(xid)}) || ticket.assignedPublicXID() != xid {
			t.Fatal("phase reset discarded real assigned ownership", after)
		}
		e.mu.Lock()
		e.recordCommitTimestampLocked(ticket.ref)
		if ticket.assignedPublicXID() != xid {
			e.mu.Unlock()
			t.Fatal("bookkeeping impersonated manager release")
		}
		e.resetVisibilityPhaseLocked(ticket.ref)
		after = e.visibility[ticket.ref]
		e.mu.Unlock()
		if after != (transactionVisibility{ticket: ticket}) || ticket.assignedPublicXID() != 0 {
			t.Fatal("completed phase retained XID or lost association", after)
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, ticket, pgTransactionLock)) != 0 || len(pgLocksUnitOwnerRecords(records, ticket, pgVirtualLock)) != 1 {
			t.Fatal("phase retirement changed VXID or retained completed XID", records)
		}
		next := pgLocksUnitAssign(t, e, ticket)
		if next <= xid {
			t.Fatal("later phase reused completed XID", xid, next)
		}
	})
	t.Run("public_backend_boundary", func(t *testing.T) {
		e := New()
		ref := TxRef{owner: e, session: SessionToken{owner: e, id: math.MaxInt32 + 1}, id: 1}
		if _, _, err := publicLockOwner(e, ref, false); err == nil || err.Error() != "stuntgres: lock session identity exceeds integer range" {
			t.Fatal("PID overflow", err)
		}
		if owner, ok, err := publicLockOwner(e, ref, true); err != nil || !ok || !owner.prepared {
			t.Fatal("prepared PID must be NULL", owner, ok, err)
		}
		ref.session = SessionToken{owner: e, private: &Session{}}
		if _, ok, err := publicLockOwner(e, ref, false); err != nil || ok {
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
	ref := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: d.address}
	store := e.storage[d.address]
	scope.lockedRef, scope.lockedStorage, scope.lockedDescriptor = ref, store, d
	before := e.assignedXID
	switch route {
	case "insert":
		v, _ := sqltype.Integer(testUsage(e, sqltype.Int4).Type, 1)
		err = scope.Writes().Apply(ref, [][]sqltype.Value{{v, sqltype.String(testUsage(e, sqltype.Text).Type, "owned")}})
	case "row_grant":
		_, err = scope.acquireQueryRow(queryLeaf{ref: ref, relation: d, storage: store}, 1, queryRowMark{mode: rowUpdate, wait: parser.RowLockBlock})
	case "catalog":
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		next := *d
		next.revision++
		if err = stage.Replace(&next); err == nil {
			var publication *PreparedCatalogPublication
			publication, err = e.ValidateCatalogStage(context.Background(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(op, scope, publication, PublicationToken{Epoch: ref.Epoch})
			}
		}
	case "prepare":
		e.prepared = make(map[string]*preparedTransaction)
		entry := &preparedTransaction{ref: ticket.ref, ticket: ticket, gid: "pg_locks_unit_prepare"}
		err = e.publishRowsPrepared(op, ticket.ref, nil, nil, nil, nil, &preparedPublication{entry: entry, preparing: true, origin: scope.owner}, scope)
		if err == nil {
			e.locks.detachPrepared(ticket)
		}
	}
	if err != nil {
		t.Fatal(route, err)
	}
	if uint64(ticket.assignedPublicXID()) != before+1 || e.assignedXID != before+1 {
		t.Fatal("actual route failed to mirror exactly one canonical assignment", route, before, ticket.assignedPublicXID(), e.assignedXID)
	}
	if len(pgLocksUnitOwnerRecords(pgLocksUnitCapture(t, e), ticket, pgTransactionLock)) != 1 {
		t.Fatal("actual assignment lacks completion ownership", route)
	}
	if route == "prepare" {
		delete(e.prepared, "pg_locks_unit_prepare")
		e.locks.end(ticket)
	}
}

func TestPGLocksPredicateLifetime(t *testing.T) {
	e, _, d := typedUnitCatalog(t, false)
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enrollSerializableLocked(a.ref, Serializable)
	e.enrollSerializableLocked(b.ref, Serializable)
	first, overlap := e.serializable.nodes[a.ref], e.serializable.nodes[b.ref]
	first.captured, overlap.captured = true, true
	first.reads[d.address] = struct{}{}
	before, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(before) != 1 {
		t.Fatal("real SSI read", before, err)
	}
	first.committed = true
	e.endSerializableLocked(a.ref, true)
	after, err := e.capturePGPredicateLocksLocked()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("committed overlap changed original owner", before, after, err)
	}
	first.prepared = true
	prepared, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(prepared) != 1 || !prepared[0].owner.prepared {
		t.Fatal("prepared SSI association", prepared, err)
	}
	first.aborted = true
	aborted, err := e.capturePGPredicateLocksLocked()
	if err != nil || len(aborted) != 0 {
		t.Fatal("aborted predicate leaked", aborted, err)
	}
	e.endSerializableLocked(b.ref, false)
	if records, err := e.capturePGPredicateLocksLocked(); err != nil || len(records) != 0 {
		t.Fatal("overlap cleanup", records, err)
	}
	e.fenceSerializableLocked()
}
