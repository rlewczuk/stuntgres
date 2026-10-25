package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestMergeUpdateRowLocks(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "cancel", "reset"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "UPDATE t SET v=20")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, worker, ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND t.v>=20 THEN UPDATE SET v=100 WHEN MATCHED THEN UPDATE SET v=200", false)
			enginetest.LockPending(t, e, 1)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.Locks.Inspect()
			rowWait := false
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == ref && resource.Row != 0 && len(entry.Pending) == 1 {
					rowWait = true
				}
			}
			if !rowWait {
				t.Fatal("no actual row queue")
			}
			switch finish {
			case "cancel":
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.TransactionEnd(t, holder, tx, false)
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, holder, tx, false)
				enginetest.LockEmpty(t, e)
				return
			default:
				enginetest.TransactionEnd(t, holder, tx, finish == "commit")
				enginetest.LockAwait(t, done, nil)
			}
			want := int64(10)
			if finish == "commit" {
				want = 100
			}
			if finish == "rollback" {
				want = 200
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", want)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestMergeUpdatePublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	before := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 || result.Rows != nil {
		scope.Finish(errors.New("bad result"))
		t.Fatal(result)
	}
	if !reflect.DeepEqual(before, e.Storage.Relations[ref.Address].Versions) {
		scope.Finish(errors.New("early publish"))
		t.Fatal("Execute escaped private stage")
	}
	sentinel := errors.New("late owner rejection")
	if err = scope.Finish(sentinel); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, e.Storage.Relations[ref.Address].Versions) {
		t.Fatal("failed Finish published mixed prefix")
	}
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	enginetest.LockEmpty(t, e)
}

func TestMergeUpdateHeldACL(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO source VALUES(1)"} {
		enginetest.RoleExecute(t, e, admin, q)
	}
	owner := admin.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("merge_update_actor")
	grant := func(table string, attr int, mask authorization.Privileges) {
		ref := enginetest.RoleTableRef(t, e, table)
		ref.Address.SubID = int32(attr)
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 1, authorization.SelectPrivilege)
	grant("t", 2, authorization.UpdatePrivilege)
	grant("source", 1, authorization.SelectPrivilege)
	actorOp, actorDone := enginetest.RoleOperation(t, e, "merge_update_actor")
	defer actorDone()
	ast := enginetest.Statement(t, "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20")
	description, err := e.Describe(actorOp, ast)
	if err != nil {
		t.Fatal(err)
	}
	execute := func(denied string) {
		scope, err := e.StartStatement(actorOp)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		r, err := scope.Execute(ast, description.Parameters.Count, nil)
		err = actorOp.CommandError(scope.Finish(err))
		if denied == "" {
			if err != nil || r.Count != 1 || r.Rows != nil {
				t.Fatal(r, err)
			}
			return
		}
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table " + denied}
		if !errors.As(err, &d) || !reflect.DeepEqual(*d, want) || r.Count != 0 || r.Rows != nil {
			t.Fatal(r, err)
		}
	}
	execute("")
	grant("t", 2, 0)
	execute("t")
	grant("t", 2, authorization.UpdatePrivilege)
	execute("")
	grant("source", 1, 0)
	execute("source")
	grant("source", 1, authorization.SelectPrivilege)
	execute("")
	enginetest.LockEmpty(t, e)
}
