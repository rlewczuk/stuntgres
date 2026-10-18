package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// All waits are observed through the actual manager queue. Register cancellation
// and joining after session cleanup so workers finish before their sessions end.
func indexMaintenanceAsync(t *testing.T, e *Engine, s *Session, q string) (context.CancelFunc, func() error) {
	t.Helper()
	stmt := statement(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			result, executeErr := e.Execute(op, stmt, 0, nil)
			err = executeErr
			if result.Rows != nil {
				result.Rows.Close()
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("index utility did not terminate during cleanup")
			}
		}
	})
	return cancel, func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("index utility did not terminate")
			return nil
		}
	}
}

func TestIndexMaintenanceDropParentAddressOrdering(t *testing.T) {
	e := New()
	holder, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(n INT)", "CREATE TABLE b(n INT)", "CREATE INDEX ai ON a(n)", "CREATE INDEX ai2 ON a(n)", "CREATE INDEX bi ON b(n)"} {
		sessionSQL(t, holder, q)
	}
	a, b := roleTableRef(t, e, "a"), roleTableRef(t, e, "b")
	ai, ai2, bi := roleTableRef(t, e, "ai"), roleTableRef(t, e, "ai2"), roleTableRef(t, e, "bi")
	tx := transactionBegin(t, holder)
	if err := e.locks.acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: b}, accessShare); err != nil {
		t.Fatal(err)
	}
	before := indexMaintenanceCapture(e)
	cancel, await := indexMaintenanceAsync(t, e, utility, "DROP INDEX bi,ai2,ai,ai CASCADE")
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	pending := e.locks.entries[lockResource{ObjectRef: b}].pending[0]
	first := e.locks.entries[lockResource{ObjectRef: a}]
	ordered := pending.mode == accessExclusive && first != nil && first.holders[pending.ticket].mode == accessExclusive
	for _, ref := range []ObjectRef{ai, ai2, bi} {
		ordered = ordered && e.locks.entries[lockResource{ObjectRef: ref}] == nil
	}
	e.locks.mu.Unlock()
	if !ordered {
		t.Fatal("DROP did not acquire all sorted parents before any selected index")
	}
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
	transactionEnd(t, holder, tx, false)
	lockEmpty(t, e)
	sessionSQL(t, utility, "DROP INDEX bi,ai2,ai,ai CASCADE")
	if len(e.indexRoots) != 0 {
		t.Fatal("deduplicated multi-parent drop left roots")
	}
	lockEmpty(t, e)
}

func TestIndexMaintenanceExactLockModes(t *testing.T) {
	for _, c := range []struct {
		name, q, resource string
		held, want        relationLockMode
		parent            bool
	}{
		{"index_rebuild", "REINDEX INDEX ix", "ix", accessShare, accessExclusive, true},
		{"table_rebuild", "REINDEX TABLE t", "ix", accessShare, accessExclusive, true},
		{"index_rename", "ALTER INDEX ix RENAME TO renamed", "ix", relationShare, shareUpdateExclusive, false},
		{"table_alias", "ALTER INDEX t RENAME TO renamed", "t", accessShare, accessExclusive, false},
		{"index_drop", "DROP INDEX ix", "t", accessShare, accessExclusive, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New()
			holder, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "CREATE INDEX ix ON t(n)", "CREATE INDEX sibling ON t(n)"} {
				sessionSQL(t, holder, q)
			}
			table := roleTableRef(t, e, "t")
			ref := roleTableRef(t, e, c.resource)
			sibling := roleTableRef(t, e, "sibling")
			tx := transactionBegin(t, holder)
			if err := e.locks.acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: ref}, c.held); err != nil {
				t.Fatal(err)
			}
			cancel, await := indexMaintenanceAsync(t, e, utility, c.q)
			lockPending(t, e, 1)
			e.locks.mu.Lock()
			request := e.locks.entries[lockResource{ObjectRef: ref}].pending[0]
			correct := request.mode == c.want
			if c.parent {
				entry := e.locks.entries[lockResource{ObjectRef: table}]
				correct = correct && entry != nil && entry.holders[request.ticket].mode == relationShare
			} else if c.resource != "t" {
				correct = correct && e.locks.entries[lockResource{ObjectRef: table}] == nil
			}
			correct = correct && e.locks.entries[lockResource{ObjectRef: sibling}] == nil
			e.locks.mu.Unlock()
			if !correct {
				t.Fatal("utility mode/resource order broadened or omitted parent Share")
			}
			cancel()
			if err := await(); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
		})
	}
}

func TestIndexMaintenanceGrantIncludesCommittedWriterOnly(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			writer, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
				sessionSQL(t, writer, q)
			}
			index, old := btreeRoot(t, e, "ix")
			tx := transactionBegin(t, writer)
			sessionSQL(t, writer, "INSERT INTO t VALUES(2),(2)")
			_, await := indexMaintenanceAsync(t, e, utility, "REINDEX INDEX ix")
			lockPending(t, e, 1)
			e.locks.mu.Lock()
			parent := ObjectRef{owner: e.domain, Epoch: tx.epoch, Address: index.sqlIndex.parent}
			request := e.locks.entries[lockResource{ObjectRef: parent}].pending[0]
			share := request.mode == relationShare
			e.locks.mu.Unlock()
			if !share {
				t.Fatal("rebuild did not wait for writer through Share admission")
			}
			transactionEnd(t, writer, tx, commit)
			if err := await(); err != nil {
				t.Fatal(err)
			}
			if commit {
				btreeWantKeys(t, e, "ix", "1", "2", "2")
			} else {
				btreeWantKeys(t, e, "ix", "1")
			}
			if e.indexRoots[index.address] == old {
				t.Fatal("waited rebuild reused predecessor")
			}
			if got := btreeKeys(t, old); len(got) != 1 || got[0] != "1" {
				t.Fatal("writer/rebuild mutated retained root", got)
			}
			lockEmpty(t, e)
		})
	}
}

// Low-level catalog publication deliberately changes metadata while a SQL
// utility sleeps. This exercises exact-incarnation/grant fences without asking
// a second SQL utility to bypass the same conflicting relation locks.
func indexMaintenancePublishIndexClone(t *testing.T, e *Engine, name, newName string, remove bool) {
	t.Helper()
	old, _ := btreeRoot(t, e, name)
	if old.sqlIndex.unique {
		t.Fatal("fixture requires an ordinary index")
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if remove {
		if err := stage.Remove(ObjectRef{owner: e.domain, Epoch: base.Epoch(), Address: old.address}); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	oid, err := e.allocateOIDLocked()
	var revision DefinitionRevision
	if err == nil {
		revision, err = e.revisionLocked()
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.address.ObjectID = oid
	next.name = newName
	next.revision = revision
	next.columns = append([]ColumnDescriptor(nil), old.columns...)
	for i := range next.columns {
		next.columns[i].OriginTable = uint32(oid)
	}
	if err := stage.Create(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func TestIndexMaintenanceGrantIncarnationIndexSetAndEpoch(t *testing.T) {
	for _, action := range []string{"recreate", "new_index", "reset"} {
		t.Run(action, func(t *testing.T) {
			e := New()
			writer, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
				sessionSQL(t, writer, q)
			}
			original, _ := btreeRoot(t, e, "ix")
			tx := transactionBegin(t, writer)
			sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			q := "REINDEX INDEX ix"
			if action == "new_index" {
				q = "REINDEX TABLE t"
			}
			_, await := indexMaintenanceAsync(t, e, utility, q)
			lockPending(t, e, 1)
			switch action {
			case "recreate":
				indexMaintenancePublishIndexClone(t, e, "ix", "ix", true)
			case "new_index":
				indexMaintenancePublishIndexClone(t, e, "ix", "new_index", false)
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before := indexMaintenanceCapture(e)
			transactionEnd(t, writer, tx, false)
			if err := await(); !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("grant retargeted or accepted changed index set/epoch", err)
			}
			indexMaintenanceUnchanged(t, e, before)
			if action == "recreate" {
				current, root := btreeRoot(t, e, "ix")
				if current.address == original.address || root != before.roots[current.address] {
					t.Fatal("old grant affected recreated name")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestIndexMaintenanceMaintainRevokedDuringWait(t *testing.T) {
	e := New()
	admin, actor := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE USER maintainer", "CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
		sessionSQL(t, admin, q)
	}
	table := roleTableRef(t, e, "t")
	owner := e.state.descriptors[table.Address].(*relationDescriptor).owner
	user, ok := e.Roles().Lookup("maintainer")
	if !ok {
		t.Fatal("missing role fixture")
	}
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.MaintainPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, actor, "SET SESSION AUTHORIZATION maintainer")
	tx := transactionBegin(t, admin)
	sessionSQL(t, admin, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	_, await := indexMaintenanceAsync(t, e, actor, "REINDEX INDEX ix")
	lockPending(t, e, 1)
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	before := indexMaintenanceCapture(e)
	transactionEnd(t, admin, tx, false)
	err := await()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for index ix" {
		t.Fatal("grant failed to reauthorize parent MAINTAIN", diagnostic, err)
	}
	indexMaintenanceUnchanged(t, e, before)
	lockEmpty(t, e)
}

func TestIndexMaintenancePrivilegeAdmission(t *testing.T) {
	e := New()
	admin, actor := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE USER maintainer", "CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
		sessionSQL(t, admin, q)
	}
	table := roleTableRef(t, e, "t")
	owner := e.state.descriptors[table.Address].(*relationDescriptor).owner
	user, ok := e.Roles().Lookup("maintainer")
	if !ok {
		t.Fatal("missing role fixture")
	}
	namespace := ObjectRef{e.domain, e.state.epoch, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, actor, "SET SESSION AUTHORIZATION maintainer")
	tx := transactionBegin(t, admin)
	sessionSQL(t, admin, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	before := indexMaintenanceCapture(e)
	for _, c := range []struct{ query, message string }{
		{"REINDEX INDEX ix", "permission denied for index ix"},
		{"REINDEX TABLE t", "permission denied for table t"},
		{"ALTER INDEX ix RENAME TO fresh", "must be owner of index ix"},
		{"DROP INDEX ix", "must be owner of index ix"},
	} {
		_, await := indexMaintenanceAsync(t, e, actor, c.query)
		err := await()
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: c.message}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal("denied admission waited or lost complete diagnostic", c.query, diagnostic, err)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	transactionEnd(t, admin, tx, false)
	lockEmpty(t, e)
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.MaintainPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t"} {
		index, old := btreeRoot(t, e, "ix")
		sessionSQL(t, actor, q)
		next, fresh := btreeRoot(t, e, "ix")
		if fresh == old || next.address != index.address || len(fresh.entries) != 1 {
			t.Fatal("real parent MAINTAIN did not rebuild populated root", q, old, fresh)
		}
		lockEmpty(t, e)
	}
}

func TestIndexMaintenanceGrantCompatibleRevision(t *testing.T) {
	for _, name := range []string{"t", "ix"} {
		t.Run(name, func(t *testing.T) {
			e := New()
			writer, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(1)", "CREATE INDEX ix ON t(n)"} {
				sessionSQL(t, writer, q)
			}
			index, oldRoot := btreeRoot(t, e, "ix")
			tx := transactionBegin(t, writer)
			sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			_, await := indexMaintenanceAsync(t, e, utility, "REINDEX INDEX ix")
			lockPending(t, e, 1)
			base := e.CatalogRead()
			ref := roleTableRef(t, e, name)
			next := *base.state.descriptors[ref.Address].(*relationDescriptor)
			next.revision++
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			if e.indexRoots[index.address] != oldRoot {
				t.Fatal("compatible fixture revision rebuilt a root")
			}
			transactionEnd(t, writer, tx, false)
			if err := await(); err != nil {
				t.Fatal("same-incarnation compatible revision was rejected", err)
			}
			if e.state.descriptors[ref.Address] != &next || e.indexRoots[index.address] == oldRoot {
				t.Fatal("grant did not retain the current descriptor and publish a real fresh root")
			}
			btreeWantKeys(t, e, "ix", "1", "2")
			lockEmpty(t, e)
		})
	}
}
