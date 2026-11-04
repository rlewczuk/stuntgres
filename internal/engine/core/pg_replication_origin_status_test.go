package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPGReplicationOriginStatusCapture(t *testing.T) {
	e := engine.New()
	h, finish := originStateUnitHost(t, e)
	created, err := originStateUnitCall(t, e, h, functions.ReplicationOriginCreate, "capture_status", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	id, err := created.OID()
	if err != nil {
		t.Fatal(err)
	}
	finish(nil)
	h, finish = originStateUnitHost(t, e)
	if _, err := originStateUnitCall(t, e, h, functions.ReplicationOriginAdvance, "capture_status", ^uint64(0), false); err != nil {
		t.Fatal(err)
	}
	finish(nil)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ReplicationOriginStatusOID}
	produce, ok := pgcatalog.ProducerFor(address.ObjectID)
	if !ok {
		t.Fatal("missing producer")
	}
	// This is the same single mutex window as the statement capture. Both
	// immutable registration roots and the only necessary mutable-map copy
	// are selected before releasing cluster publication authority.
	e.Mu.Lock()
	view := e.Catalog.Current()
	progress := e.CopyReplicationOriginProgressLocked()
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	e.Mu.Unlock()
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: roles, Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: view.Get(address).(*catalog.Relation), ReplicationOriginProgress: progress}
	retained, err := catalogrt.CaptureRows(in, produce)
	if err != nil || len(retained.Anonymous) != 1 {
		t.Fatal(retained, err)
	}
	for _, kind := range []functions.ReplicationOriginKind{functions.ReplicationOriginAdvance, functions.ReplicationOriginDrop} {
		h, finish = originStateUnitHost(t, e)
		_, err := originStateUnitCall(t, e, h, kind, "capture_status", 2, false)
		finish(err)
		if err != nil {
			t.Fatal(err)
		}
		old, err := catalogrt.CaptureRows(in, produce)
		if err != nil || len(old.Anonymous) != 1 {
			t.Fatal(old, err)
		}
		remote, err := old.Anonymous[0][2].PGLSN()
		name, nameErr := old.Anonymous[0][1].Type().Output(t.Context(), old.Anonymous[0][1])
		if err != nil || remote != ^uint64(0) || nameErr != nil || name != "capture_status" || progress[ids.OID(id)] != ^uint64(0) {
			t.Fatal("live publication aliased old capture", remote, name, err, nameErr)
		}
	}
	e.Mu.Lock()
	fresh := e.CopyReplicationOriginProgressLocked()
	e.Mu.Unlock()
	if len(fresh) != 0 {
		t.Fatal("drop did not clear live slot", fresh)
	}
	in.ReplicationOriginProgress = fresh
	rows, err := produce(in)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	// A detached map can be mutated by its owner without touching the live
	// cluster. The producer itself never mutates either map.
	progress[ids.OID(id)] = 9
	e.Mu.Lock()
	live := e.CopyReplicationOriginProgressLocked()
	e.Mu.Unlock()
	if len(live) != 0 {
		t.Fatal("detached copy became live")
	}
	oldRemote, err := retained.Anonymous[0][2].PGLSN()
	if err != nil || oldRemote != ^uint64(0) {
		t.Fatal("retained row borrowed map", oldRemote, err)
	}
	foreign := engine.New()
	in.Catalog = foreign.CatalogRead()
	if rows, err := produce(in); err == nil || rows != nil {
		t.Fatal("foreign roots accepted", rows, err)
	}
	// Zero capacity and an unavailable cluster both read empty without
	// acquiring scalar-command prerequisite errors or allocating any slots.
	for _, nilCluster := range []bool{false, true} {
		empty := engine.New()
		empty.Settings = settings.NewRegistry([]settings.Definition{{Name: "max_active_replication_origins", Type: settings.Integer, BootDefault: "0"}}, false)
		if nilCluster {
			empty.Cluster = nil
		}
		empty.Mu.Lock()
		emptyView := empty.Catalog.Current()
		absent := empty.CopyReplicationOriginProgressLocked()
		emptyRoles := empty.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		empty.Mu.Unlock()
		if absent != nil {
			t.Fatal(absent)
		}
		value := pgcatalog.Input{Context: t.Context(), Catalog: emptyView, Roles: emptyRoles, Owner: emptyView.Owner(), Ref: empty.Catalog.Domain.Ref(emptyView.Epoch(), address), Source: emptyView.Get(address).(*catalog.Relation)}
		if rows, err := produce(value); err != nil || len(rows) != 0 {
			t.Fatal(rows, err)
		}
	}
	// A real transaction-private registration must resolve against its own
	// admitted catalog while progress remains immediate cluster state.
	s := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	if _, err := originStateUnitCall(t, e, core.HostOf(scope), functions.ReplicationOriginCreate, "private_status", 0, false); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	if _, err := originStateUnitCall(t, e, core.HostOf(scope), functions.ReplicationOriginAdvance, "private_status", 7, false); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	op = enginetest.TransactionOperation(t, s)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	private := pgcatalog.Input{Context: t.Context(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: auth.Owner, Ref: auth.Ref(address), Source: auth.Catalog.Get(address).(*catalog.Relation), ReplicationOriginProgress: e.CopyReplicationOriginProgressLocked()}
	committed := e.Catalog.Current()
	e.Mu.Unlock()
	op.Done()
	if private.Catalog.State() == committed.State() || committed.NameAddress(catalog.ReplicationOriginNameKey("private_status")) != (ids.ObjectAddress{}) {
		t.Fatal("private registration published early")
	}
	if rows, err := produce(private); err != nil || len(rows) != 1 || rows[0][1].IsNull() {
		t.Fatal("private capture rejected", rows, err)
	}
	op = enginetest.TransactionOperation(t, s)
	_, err = e.Rollback(op, tx)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := produce(private); err != nil || len(rows) != 1 || rows[0][1].IsNull() {
		t.Fatal("rollback mutated detached private capture", rows, err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	in.Catalog = e.CatalogRead()
	if rows, err := produce(in); err == nil || rows != nil {
		t.Fatal("stale source/ref accepted after replacement", rows, err)
	}
	t.Run("initialization_authority", func(t *testing.T) {
		host := engine.New()
		admin := enginetest.StartSession(t, host, nil)
		actor := enginetest.StartSession(t, host, nil)
		statsFunctionExec(t, admin, "CREATE ROLE status_capture_actor",
			"GRANT SELECT ON pg_catalog.pg_replication_origin_status TO status_capture_actor")
		statsFunctionExec(t, actor, "SET ROLE status_capture_actor")
		ast, description := enginetest.PreparedDescription(t, host, actor, "SELECT * FROM pg_catalog.pg_replication_origin_status LIMIT 0")
		for _, allowed := range []bool{false, true, false} {
			if allowed {
				statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() TO status_capture_actor")
			} else {
				statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_capture_actor")
			}
			result, err := enginetest.PreparedExecute(host, actor, ast, &description)
			if allowed {
				if err != nil || result.Rows == nil {
					t.Fatal("admitted initialization", result, err)
				}
				result.Rows.Close()
			} else {
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("denied initialization published a source")
				}
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_show_replication_origin_status"})
			}
			host.Mu.Lock()
			progress := host.CopyReplicationOriginProgressLocked()
			host.Mu.Unlock()
			if progress != nil {
				t.Fatal("LIMIT initialization allocated replication progress", progress)
			}
		}
	})
}
