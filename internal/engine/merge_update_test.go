package engine

import "stuntgres/internal/engine/authorization"

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMergeUpdateRowLocks(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "cancel", "reset"} {
		t.Run(finish, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "UPDATE t SET v=20")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, worker, ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND t.v>=20 THEN UPDATE SET v=100 WHEN MATCHED THEN UPDATE SET v=200", false)
			lockPending(t, e, 1)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			e.locks.mu.Lock()
			rowWait := false
			for resource, entry := range e.locks.entries {
				if resource.ObjectRef == ref && resource.row != 0 && len(entry.pending) == 1 {
					rowWait = true
				}
			}
			e.locks.mu.Unlock()
			if !rowWait {
				t.Fatal("no actual row queue")
			}
			switch finish {
			case "cancel":
				cancel()
				lockAwait(t, done, context.Canceled)
				transactionEnd(t, holder, tx, false)
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ErrCatalogReplaced)
				transactionEnd(t, holder, tx, false)
				lockEmpty(t, e)
				return
			default:
				transactionEnd(t, holder, tx, finish == "commit")
				lockAwait(t, done, nil)
			}
			want := int64(10)
			if finish == "commit" {
				want = 100
			}
			if finish == "rollback" {
				want = 200
			}
			transactionRows(t, peer, "SELECT v FROM t", want)
			lockEmpty(t, e)
		})
	}
}

func TestMergeUpdatePublication(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	before := append([]rowVersion(nil), e.storage[ref.Address].versions...)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 || result.Rows != nil {
		scope.Finish(errors.New("bad result"))
		t.Fatal(result)
	}
	if !reflect.DeepEqual(before, e.storage[ref.Address].versions) {
		scope.Finish(errors.New("early publish"))
		t.Fatal("Execute escaped private stage")
	}
	sentinel := errors.New("late owner rejection")
	if err = scope.Finish(sentinel); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, e.storage[ref.Address].versions) {
		t.Fatal("failed Finish published mixed prefix")
	}
	transactionRows(t, peer, "SELECT v FROM t", 10)
	lockEmpty(t, e)
}

func TestMergeUpdateHeldACL(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO source VALUES(1)"} {
		roleExecute(t, e, admin, q)
	}
	owner := admin.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("merge_update_actor")
	grant := func(table string, attr int, mask authorization.Privileges) {
		ref := roleTableRef(t, e, table)
		ref.Address.SubID = int32(attr)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 1, authorization.SelectPrivilege)
	grant("t", 2, authorization.UpdatePrivilege)
	grant("source", 1, authorization.SelectPrivilege)
	actorOp, actorDone := roleOperation(t, e, "merge_update_actor")
	defer actorDone()
	ast := statement(t, "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20")
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
	lockEmpty(t, e)
}
