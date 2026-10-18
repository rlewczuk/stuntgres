package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestUpdateFromStoredSourceMarksOwnFrontier(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		e := New()
		holder := sessionStart(t, e, nil)
		writer := sessionStart(t, e, nil)
		peer := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1,100)", "CREATE VIEW marked AS SELECT k,n FROM s FOR UPDATE", "CREATE VIEW wrapped AS SELECT k,n FROM marked"} {
			sessionSQL(t, peer, q)
		}
		tx := transactionBegin(t, holder)
		sessionSQL(t, holder, "UPDATE s SET n=101")
		ctx, cancel := context.WithCancel(context.Background())
		done := lockAsync(e, writer, ctx, "UPDATE t SET v=t.v+wrapped.n FROM wrapped WHERE t.id=wrapped.k RETURNING old.v,new.v,wrapped.n", false)
		lockPending(t, e, 1)
		sref := catalogRef(t, e, e.CatalogRead(), "s")
		lockState := e.locks.Inspect()
		sourceWait := false
		for resource, entry := range lockState.Resources {
			if resource.ObjectRef == sref && resource.Row != 0 && len(entry.Pending) == 1 {
				sourceWait = true
			}
		}
		if !sourceWait {
			t.Fatal("stored source mark was not consumed at its real view frontier")
		}
		// Target capture precedes the source-view mark's wait. The late target
		// occurrence may not join after the mark refreshes its physical source.
		sessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
		if cancelled {
			cancel()
			lockAwait(t, done, context.Canceled)
		}
		transactionEnd(t, holder, tx, true)
		if !cancelled {
			lockAwait(t, done, nil)
		}
		cancel()
		if cancelled {
			transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 10, 30)
		} else {
			transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 30, 111)
		}
		transactionRows(t, peer, "SELECT n FROM s", 101)
		lockEmpty(t, e)
	}
}

func TestUpdateFromStoredInvokerMarksRequireBaseUpdate(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER marked_writer", "CREATE TABLE t(a INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(7)", "CREATE VIEW marked WITH (security_invoker=true) AS SELECT k FROM s FOR UPDATE"} {
		roleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("marked_writer")
	grant := func(table string, mask authorization.Privileges) {
		t.Helper()
		ref := roleTableRef(t, e, table)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", authorization.UpdatePrivilege)
	grant("s", authorization.SelectPrivilege)
	grant("marked", authorization.SelectPrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION marked_writer")
	roleError(t, e, op, "UPDATE t SET a=marked.k FROM marked RETURNING marked.k", "42501", "permission denied for table s", "")
	grant("s", authorization.SelectPrivilege|authorization.UpdatePrivilege)
	result := roleExecute(t, e, op, "UPDATE t SET a=marked.k FROM marked RETURNING marked.k")
	if result.Count != 1 {
		t.Fatal(result)
	}
	result.Rows.Close()
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	result = roleExecute(t, e, op, "SELECT a FROM t")
	rows := tuples(result)
	result.Rows.Close()
	if len(rows) != 1 || rows[0][0] != int64(7) {
		t.Fatal(rows)
	}
	lockEmpty(t, e)
}
