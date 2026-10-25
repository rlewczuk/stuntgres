package indexcmds_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// All waits are observed through the actual manager queue. Register cancellation
// and joining after session cleanup so workers finish before their sessions end.
func indexMaintenanceAsync(t *testing.T, e *engine.Engine, s *session.Session, q string) (context.CancelFunc, func() error) {
	t.Helper()
	stmt := enginetest.Statement(t, q)
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
	e := engine.New()
	holder, utility := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(n INT)", "CREATE TABLE b(n INT)", "CREATE INDEX ai ON a(n)", "CREATE INDEX ai2 ON a(n)", "CREATE INDEX bi ON b(n)"} {
		enginetest.SessionSQL(t, holder, q)
	}
	a, b := enginetest.RoleTableRef(t, e, "a"), enginetest.RoleTableRef(t, e, "b")
	ai, ai2, bi := enginetest.RoleTableRef(t, e, "ai"), enginetest.RoleTableRef(t, e, "ai2"), enginetest.RoleTableRef(t, e, "bi")
	tx := enginetest.TransactionBegin(t, holder)
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: b}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	before := indexMaintenanceCapture(e)
	cancel, await := indexMaintenanceAsync(t, e, utility, "DROP INDEX bi,ai2,ai,ai CASCADE")
	enginetest.LockPending(t, e, 1)
	lockState := e.Locks.Inspect()
	pending := lockState.Resources[lock.Resource{ObjectRef: b}].Pending[0]
	first := lockState.Resources[lock.Resource{ObjectRef: a}]
	ordered := pending.Mode == lock.AccessExclusive && first != nil && first.Holders[pending.Ticket].Mode == lock.AccessExclusive
	for _, ref := range []ids.ObjectRef{ai, ai2, bi} {
		ordered = ordered && lockState.Resources[lock.Resource{ObjectRef: ref}] == nil
	}
	if !ordered {
		t.Fatal("DROP did not acquire all sorted parents before any selected index")
	}
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	indexMaintenanceUnchanged(t, e, before)
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, utility, "DROP INDEX bi,ai2,ai,ai CASCADE")
	if len(e.Storage.IndexRoots) != 0 {
		t.Fatal("deduplicated multi-parent drop left roots")
	}
	enginetest.LockEmpty(t, e)
}

func TestIndexMaintenanceExactLockModes(t *testing.T) {
	for _, c := range []struct {
		name, q, resource string
		held, want        lock.Mode
		parent            bool
	}{
		{"index_rebuild", "REINDEX INDEX ix", "ix", lock.AccessShare, lock.AccessExclusive, true},
		{"table_rebuild", "REINDEX TABLE t", "ix", lock.AccessShare, lock.AccessExclusive, true},
		{"index_rename", "ALTER INDEX ix RENAME TO renamed", "ix", lock.Share, lock.ShareUpdateExclusive, false},
		{"table_alias", "ALTER INDEX t RENAME TO renamed", "t", lock.AccessShare, lock.AccessExclusive, false},
		{"index_drop", "DROP INDEX ix", "t", lock.AccessShare, lock.AccessExclusive, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := engine.New()
			holder, utility := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "CREATE INDEX ix ON t(n)", "CREATE INDEX sibling ON t(n)"} {
				enginetest.SessionSQL(t, holder, q)
			}
			table := enginetest.RoleTableRef(t, e, "t")
			ref := enginetest.RoleTableRef(t, e, c.resource)
			sibling := enginetest.RoleTableRef(t, e, "sibling")
			tx := enginetest.TransactionBegin(t, holder)
			if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, c.held); err != nil {
				t.Fatal(err)
			}
			cancel, await := indexMaintenanceAsync(t, e, utility, c.q)
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			request := lockState.Resources[lock.Resource{ObjectRef: ref}].Pending[0]
			correct := request.Mode == c.want
			if c.parent {
				entry := lockState.Resources[lock.Resource{ObjectRef: table}]
				correct = correct && entry != nil && entry.Holders[request.Ticket].Mode == lock.Share
			} else if c.resource != "t" {
				correct = correct && lockState.Resources[lock.Resource{ObjectRef: table}] == nil
			}
			correct = correct && lockState.Resources[lock.Resource{ObjectRef: sibling}] == nil
			if !correct {
				t.Fatal("utility mode/resource order broadened or omitted parent Share")
			}
			cancel()
			if err := await(); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			enginetest.TransactionEnd(t, holder, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestIndexMaintenanceGrantIncludesCommittedWriterOnly(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			writer, utility := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
				enginetest.SessionSQL(t, writer, q)
			}
			index, old := btreeRoot(t, e, "ix")
			tx := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(2),(2)")
			_, await := indexMaintenanceAsync(t, e, utility, "REINDEX INDEX ix")
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			parent := e.Catalog.Domain.Ref(tx.Epoch(), index.SQLIndex.Parent)
			request := lockState.Resources[lock.Resource{ObjectRef: parent}].Pending[0]
			share := request.Mode == lock.Share
			if !share {
				t.Fatal("rebuild did not wait for writer through Share admission")
			}
			enginetest.TransactionEnd(t, writer, tx, commit)
			if err := await(); err != nil {
				t.Fatal(err)
			}
			if commit {
				btreeWantKeys(t, e, "ix", "1", "2", "2")
			} else {
				btreeWantKeys(t, e, "ix", "1")
			}
			if e.Storage.IndexRoots[index.Address()] == old {
				t.Fatal("waited rebuild reused predecessor")
			}
			if got := btreeKeys(t, old); len(got) != 1 || got[0] != "1" {
				t.Fatal("writer/rebuild mutated retained root", got)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

// Low-level catalog publication deliberately changes metadata while a SQL
// utility sleeps. This exercises exact-incarnation/grant fences without asking
// a second SQL utility to bypass the same conflicting relation locks.
func indexMaintenancePublishIndexClone(t *testing.T, e *engine.Engine, name, newName string, remove bool) {
	t.Helper()
	old, _ := btreeRoot(t, e, name)
	if old.SQLIndex.Unique {
		t.Fatal("fixture requires an ordinary index")
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if remove {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), old.Address())); err != nil {
			t.Fatal(err)
		}
	}
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	var revision ids.DefinitionRevision
	if err == nil {
		revision, err = e.Catalog.RevisionLocked()
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.ObjectAddress.ObjectID = oid
	next.ObjectName = newName
	next.ObjectRevision = revision
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	for i := range next.Columns {
		next.Columns[i].OriginTable = uint32(oid)
	}
	if err := stage.Create(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func TestIndexMaintenanceGrantIncarnationIndexSetAndEpoch(t *testing.T) {
	for _, action := range []string{"recreate", "new_index", "reset"} {
		t.Run(action, func(t *testing.T) {
			e := engine.New()
			writer, utility := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
				enginetest.SessionSQL(t, writer, q)
			}
			original, _ := btreeRoot(t, e, "ix")
			tx := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			q := "REINDEX INDEX ix"
			if action == "new_index" {
				q = "REINDEX TABLE t"
			}
			_, await := indexMaintenanceAsync(t, e, utility, q)
			enginetest.LockPending(t, e, 1)
			switch action {
			case "recreate":
				indexMaintenancePublishIndexClone(t, e, "ix", "ix", true)
			case "new_index":
				indexMaintenancePublishIndexClone(t, e, "ix", "new_index", false)
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			before := indexMaintenanceCapture(e)
			enginetest.TransactionEnd(t, writer, tx, false)
			if err := await(); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("grant retargeted or accepted changed index set/epoch", err)
			}
			indexMaintenanceUnchanged(t, e, before)
			if action == "recreate" {
				current, root := btreeRoot(t, e, "ix")
				if current.Address() == original.Address() || root != before.roots[current.Address()] {
					t.Fatal("old grant affected recreated name")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestIndexMaintenanceMaintainRevokedDuringWait(t *testing.T) {
	e := engine.New()
	admin, actor := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE USER maintainer", "CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
		enginetest.SessionSQL(t, admin, q)
	}
	table := enginetest.RoleTableRef(t, e, "t")
	owner := e.Catalog.State.MustRelation(table.Address).Owner
	user, ok := e.Roles().Lookup("maintainer")
	if !ok {
		t.Fatal("missing role fixture")
	}
	if err := enginetest.SetObjectACL(e, context.Background(), table, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.MaintainPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, actor, "SET SESSION AUTHORIZATION maintainer")
	tx := enginetest.TransactionBegin(t, admin)
	enginetest.SessionSQL(t, admin, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	_, await := indexMaintenanceAsync(t, e, actor, "REINDEX INDEX ix")
	enginetest.LockPending(t, e, 1)
	if err := enginetest.SetObjectACL(e, context.Background(), table, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	before := indexMaintenanceCapture(e)
	enginetest.TransactionEnd(t, admin, tx, false)
	err := await()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for index ix" {
		t.Fatal("grant failed to reauthorize parent MAINTAIN", diagnostic, err)
	}
	indexMaintenanceUnchanged(t, e, before)
	enginetest.LockEmpty(t, e)
}

func TestIndexMaintenancePrivilegeAdmission(t *testing.T) {
	e := engine.New()
	admin, actor := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE USER maintainer", "CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
		enginetest.SessionSQL(t, admin, q)
	}
	table := enginetest.RoleTableRef(t, e, "t")
	owner := e.Catalog.State.MustRelation(table.Address).Owner
	user, ok := e.Roles().Lookup("maintainer")
	if !ok {
		t.Fatal("missing role fixture")
	}
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), table, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, actor, "SET SESSION AUTHORIZATION maintainer")
	tx := enginetest.TransactionBegin(t, admin)
	enginetest.SessionSQL(t, admin, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
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
	enginetest.TransactionEnd(t, admin, tx, false)
	enginetest.LockEmpty(t, e)
	if err := enginetest.SetObjectACL(e, context.Background(), table, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.MaintainPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t"} {
		index, old := btreeRoot(t, e, "ix")
		enginetest.SessionSQL(t, actor, q)
		next, fresh := btreeRoot(t, e, "ix")
		if fresh == old || next.Address() != index.Address() || len(fresh.Entries) != 1 {
			t.Fatal("real parent MAINTAIN did not rebuild populated root", q, old, fresh)
		}
		enginetest.LockEmpty(t, e)
	}
}

func TestIndexMaintenanceGrantCompatibleRevision(t *testing.T) {
	for _, name := range []string{"t", "ix"} {
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			writer, utility := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(1)", "CREATE INDEX ix ON t(n)"} {
				enginetest.SessionSQL(t, writer, q)
			}
			index, oldRoot := btreeRoot(t, e, "ix")
			tx := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			_, await := indexMaintenanceAsync(t, e, utility, "REINDEX INDEX ix")
			enginetest.LockPending(t, e, 1)
			base := e.CatalogRead()
			ref := enginetest.RoleTableRef(t, e, name)
			next := *base.MustRelation(ref.Address)
			next.ObjectRevision++
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			if e.Storage.IndexRoots[index.Address()] != oldRoot {
				t.Fatal("compatible fixture revision rebuilt a root")
			}
			enginetest.TransactionEnd(t, writer, tx, false)
			if err := await(); err != nil {
				t.Fatal("same-incarnation compatible revision was rejected", err)
			}
			if e.Catalog.State.Get(ref.Address) != &next || e.Storage.IndexRoots[index.Address()] == oldRoot {
				t.Fatal("grant did not retain the current descriptor and publish a real fresh root")
			}
			btreeWantKeys(t, e, "ix", "1", "2")
			enginetest.LockEmpty(t, e)
		})
	}
}
