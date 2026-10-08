package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCheckACLCompletedProtectedRow(t *testing.T) {
	for _, mode := range []string{"none", "readable", "all_columns", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				e := New()
				admin, done := roleOperation(t, e, "postgres")
				defer done()
				roleExecute(t, e, admin, "CREATE ROLE check_actor")
				roleExecute(t, e, admin, "CREATE TABLE t(n INT,secret TEXT,readable TEXT,CONSTRAINT c CHECK(n=1 AND secret='private'))")
				roleExecute(t, e, admin, "INSERT INTO t VALUES(1,'private','visible')")
				ref := roleTableRef(t, e, "t")
				actor, _ := e.Roles().Lookup("check_actor")
				grant := func(id int32, p Privileges) {
					target := ref
					target.Address.SubID = id
					if err := e.SetObjectACL(context.Background(), target, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: actor.Ref}, Privileges: p}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(1, UpdatePrivilege)
				detail := "Failing row contains (n) = (2)."
				switch mode {
				case "readable":
					grant(3, SelectPrivilege)
					detail = "Failing row contains (n, readable) = (2, visible)."
				case "all_columns":
					grant(1, UpdatePrivilege|SelectPrivilege)
					grant(2, SelectPrivilege)
					grant(3, SelectPrivilege)
					detail = "Failing row contains (n, secret, readable) = (2, private, visible)."
				case "table":
					grant(0, SelectPrivilege)
					detail = "Failing row contains (2, private, visible)."
				}
				op, finish := roleOperation(t, e, "check_actor")
				defer finish()
				q := "UPDATE t SET n=2"
				if prepared {
					q = "UPDATE t SET n=$1"
				}
				ast := statement(t, q)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if prepared {
					d, err := e.Describe(op, ast)
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					scope.Expected = &d
				}
				count := 0
				if prepared {
					count = 1
				}
				r, err := scope.Execute(ast, count, func(ctx context.Context, p ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					v, e := sqltype.Integer(p.Types[0].Type, 2)
					frame[0] = v
					return e
				})
				if scope.Finish(err) != err || r.Count != 0 || r.Rows != nil {
					t.Fatal(r, err)
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: detail, Schema: "public", Table: "t", Constraint: "c"})
				got := roleExecute(t, e, admin, "SELECT * FROM t")
				if !reflect.DeepEqual(tuples(got), [][]driver.Value{{int64(1), "private", "visible"}}) {
					t.Fatal(tuples(got))
				}
				got.Rows.Close()
				lockEmpty(t, e)
			})
		}
	}
}
func TestCheckCancellationBeforePublication(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(v INT CHECK(v>0))")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, scope, b, source := updateBoundSource(t, e, s, ctx, "UPDATE t SET v=v")
	calls := 0
	b.update.assignments[0] = queryProbe(b.relation.columns[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			cancel()
			return sqltype.Value{}, ctx.Err()
		}
		return sqltype.Integer(c.Row[0].Type(), 9)
	})
	r, err := e.executeUpdate(ctx, scope, b, source, nil, "public")
	if !errors.Is(err, context.Canceled) || r.Count != 0 || r.Rows != nil || calls != 2 {
		scope.Finish(err)
		op.Done()
		t.Fatal(r, err, calls)
	}
	scope.Finish(err)
	op.Done()
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2)
	lockEmpty(t, e)
	sessionSQL(t, peer, "UPDATE t SET v=7")
	lockEmpty(t, e)
}
