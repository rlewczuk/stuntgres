package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprAssignments(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TABLE holder ALTER COLUMN v SET DEFAULT ROW(99,'default')::pair`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder(k,v.i) VALUES(1,7)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder(k,v.t,w.p.i) VALUES($1,$2,$3)`, 1, 2, "z", 8)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder(k,v.i,v.t) SELECT 3,9,'source'`, 1))
		compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder ORDER BY k`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(7,)", nil}, {"(,z)", `("(8,)",)`}, {"(9,source)", nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET v.i=(v).i+1,v.t=(v).i::text WHERE k=1`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`UPDATE holder SET w.p.t=$1,w.n=$2 WHERE k=2`, 1, "nested", 4)))
		compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder ORDER BY k`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(8,7)", nil}, {"(,z)", `("(8,nested)",4)`}, {"(9,source)", nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET v.i=10,v.i=11 WHERE k=1`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET w.p=ROW(1,'first'),w.p.i=2 WHERE k=2`, 1))
		compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder ORDER BY k`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(11,7)", nil}, {"(,z)", `("(2,first)",4)`}, {"(9,source)", nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET w.p.i=3,w.p=ROW(4,'last') WHERE k=2`, 1))
		compositeExprBoth(t, p, b, c, `SELECT w FROM holder WHERE k=2`, []string{"w"}, []string{""}, [][]any{{`("(4,last)",4)`}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped_pair AS pair`, `CREATE TABLE wrapped(v wrapped_pair)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO wrapped(v.i,v.t) VALUES(1,'domain')`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE wrapped SET v.i=2,v.t='merged'`, 1))
		compositeExprBoth(t, p, b, c, `SELECT v,(v).i FROM wrapped`, []string{"v", "i"}, []string{"", "INT4"}, [][]any{{"(2,merged)", int64(2)}})
		held, err := c.PrepareContext(p.Ctx, `UPDATE holder SET v.i=$1,v.t=$2 WHERE k=3`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, x := range []struct {
			i, t any
			want string
		}{{12, "held", "(12,held)"}, {14, "fresh", "(14,fresh)"}, {nil, nil, "(,)"}, {15, "recovered", "(15,recovered)"}} {
			result, err := held.ExecContext(p.Ctx, x.i, x.t)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			compositeExprBoth(t, p, b, c, `SELECT v FROM holder WHERE k=3`, []string{"v"}, []string{""}, [][]any{{x.want}})
		}
	})
}

func TestReferenceCompositeExprMixedArrayFields(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE array_pair AS (a integer[],t varchar(3))`, `CREATE TYPE array_outer AS (p array_pair,n integer)`, `CREATE TABLE array_holder(k integer,w array_outer)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO array_holder(k,w.p.a[2],w.p.t) VALUES(1,7,'abc')`, 1))
		compositeExprBoth(t, p, b, c, `SELECT ((w).p).a::text AS a,((w).p).a[2] AS elem,(w).p.t FROM array_holder`, []string{"a", "elem", "t"}, []string{"TEXT", "INT4", "VARCHAR"}, [][]any{{"[2:2]={7}", int64(7), "abc"}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`UPDATE array_holder SET w.p.a[1]=$1,w.p.a[3]=$2`, 1, 6, 8)))
		compositeExprBoth(t, p, b, c, `SELECT ((w).p).a::text AS a,((w).p).a[1:2]::text AS sliced FROM array_holder`, []string{"a", "sliced"}, []string{"TEXT", "TEXT"}, [][]any{{"{6,7,8}", "{6,7}"}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE array_holder SET w.p.a[2:3]=ARRAY[9,10],w.p.t='xy'`, 1))
		compositeExprBoth(t, p, b, c, `SELECT ((w).p).a::text AS a,(w).p.t FROM array_holder`, []string{"a", "t"}, []string{"TEXT", "VARCHAR"}, [][]any{{"{6,9,10}", "xy"}})
		q := `UPDATE array_holder SET w.p.a[NULL]=1/0`
		fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.CompositeReferenceDiagnostic("22012", "division by zero"))
		q = `UPDATE array_holder SET w.p.a[NULL]=1/(k-k)`
		fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.CompositeReferenceDiagnostic("22004", "array subscript in assignment must not be null"))
		compositeExprBoth(t, p, b, c, `SELECT ((w).p).a::text AS a FROM array_holder`, []string{"a"}, []string{"TEXT"}, [][]any{{"{6,9,10}"}})
	})
}

func TestReferenceCompositeExprMergeUpdate(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL),(2,NULL,NULL)`, 2))
		q := `MERGE INTO holder h USING (VALUES(1,10),(2,20)) s(k,x) ON h.k=s.k WHEN MATCHED THEN UPDATE SET v.i=s.x,v.t=(h.v).i::text,w.p.i=s.x+1`
		for _, prepared := range []bool{false, true} {
			count := int64(2)
			if b.PG {
				count = 0
			}
			s := fixture.Exec(q, count)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			want := [][]any{{"(10,7)", `("(11,)",)`}, {"(20,)", `("(21,)",)`}}
			if prepared {
				want = [][]any{{"(10,10)", `("(11,)",)`}, {"(20,20)", `("(21,)",)`}}
			}
			compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder ORDER BY k`, []string{"v", "w"}, []string{"", ""}, want)
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for range 2 {
			result, err := stmt.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			count := int64(2)
			if b.PG {
				count = 0
			}
			fixture.LockAffected(t, result, count)
			compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder ORDER BY k`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(10,10)", `("(11,)",)`}, {"(20,20)", `("(21,)",)`}})
		}
	})
}

func TestReferenceCompositeExprAssignmentDiagnostics(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL)`, 1))
		for _, x := range []struct{ q, code, message, hint, base string }{
			{`UPDATE holder SET v.missing=1`, "42703", `cannot assign to field "missing" of column "v" because there is no such column in data type pair`, "", "v.missing"},
			{`UPDATE holder SET k.x=1`, "42804", `cannot assign to field "x" of column "k" because its type integer is not a composite type`, "", "k.x"},
			{`UPDATE holder SET v.i=true`, "42804", `subfield "i" is of type integer but expression is of type boolean`, `You will need to rewrite or cast the expression.`, "v.i"},
			{`UPDATE holder SET w.p.missing=1`, "42703", `cannot assign to field "missing" of column "p" because there is no such column in data type pair`, "", "w.p"},
			{`UPDATE holder SET v=ROW(1,'x'),v.i=2`, "42601", `multiple assignments to same column "v"`, "", ""},
			{`INSERT INTO holder(k,v,v.i) VALUES(2,ROW(1,'x'),2)`, "42701", `column "v" specified more than once`, "", "v.i"},
			{`INSERT INTO holder(k,v.missing) VALUES(2,1)`, "42703", `cannot assign to field "missing" of column "v" because there is no such column in data type pair`, "", "v.missing"},
		} {
			t.Run(x.q, func(t *testing.T) {
				d := fixture.CompositeReferenceDiagnostic(x.code, x.message)
				d.Hint = x.hint
				if x.base != "" {
					d.Position = compositeExprPosition(x.q, x.base)
				}
				fixture.CompositeReferenceError(t, p, c, x.q, "prepare", d)
			})
		}
		compositeExprBoth(t, p, b, c, `SELECT v FROM holder`, []string{"v"}, []string{""}, [][]any{{"(7,z)"}})
	})
}

func TestReferenceCompositeExprAtomicityAndACL(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL),(2,'(8,y)',NULL)`, 2))
		old, err := b.DB.QueryContext(p.Ctx, `SELECT (v).* FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		q := `UPDATE holder SET v.i=100,v.t=(10/(2-k))::text`
		d := fixture.CompositeReferenceDiagnostic("22012", "division by zero")
		fixture.CompositeReferenceError(t, p, c, q, "execute", d)
		q = `INSERT INTO holder(k,v.i,v.t) VALUES(3,9,'good'),(4,1/0,'bad')`
		fixture.CompositeReferenceError(t, p, c, q, "execute", d)
		compositeExprBoth(t, p, b, c, `SELECT v FROM holder ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(7,z)"}, {"(8,y)"}})
		fixture.UpdateCoreRetained(t, b.PG, old, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}, {int64(8), "y"}}, false)
		compositeExprCanceledWrites(t, p, b, c)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE ROLE composite_writer`)
		if b.PG {
			fixture.CompositeReferenceSetup(t, p, b, c, `GRANT USAGE ON SCHEMA public TO composite_writer`)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `GRANT UPDATE(v),INSERT(k,v) ON holder TO composite_writer`, `SET ROLE composite_writer`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET v.t='write'`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder(k,v.i) VALUES($1,$2)`, 1, 3, 9)))
		d = fixture.CompositeReferenceDiagnostic("42501", `permission denied for table holder`)
		fixture.CompositeReferenceError(t, p, c, `UPDATE holder SET v.i=(v).i+1`, "execute", d)
		fixture.CompositeReferenceSetup(t, p, b, c, `RESET ROLE`)
		compositeExprBoth(t, p, b, c, `SELECT v FROM holder ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(7,write)"}, {"(8,write)"}, {"(9,)"}})
		fixture.CompositeReferenceSetup(t, p, b, c, `REVOKE UPDATE(v),INSERT(k,v) ON holder FROM composite_writer`)
		if b.PG {
			fixture.CompositeReferenceSetup(t, p, b, c, `REVOKE USAGE ON SCHEMA public FROM composite_writer`)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `DROP ROLE composite_writer`)
	})
}

// pq does not promise native pre-canceled dispatch semantics. Its cancellation
// witness instead observes a real blocked PostgreSQL request before canceling.
func compositeExprCanceledWrites(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = `UPDATE holder SET v.i=99,v.t='canceled'`
	if !b.PG {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var result sql.Result
			var err error
			if prepared {
				result, err = stmt.ExecContext(ctx)
			} else {
				result, err = c.ExecContext(ctx, q)
			}
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("native pre-canceled field update result=%v error=%v", result, err)
			}
			if _, sqlError := err.(interface{ SQLState() string }); sqlError {
				t.Error("ordinary native cancellation became SQL error")
			}
		}
	} else {
		for _, prepared := range []bool{false, true} {
			func() {
				worker, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				var stmt *sql.Stmt
				if prepared {
					stmt, err = worker.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				holder, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(`LOCK TABLE holder IN ACCESS EXCLUSIVE MODE`, 0))
				pid := fixture.LockPID(t, p.Ctx, worker)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					var result sql.Result
					var err error
					if prepared {
						result, err = stmt.ExecContext(ctx)
					} else {
						result, err = worker.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.LockWait(t, p.Ctx, pid)
				var canceled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, `SELECT pg_cancel_backend($1)`, pid).Scan(&canceled); err != nil || !canceled {
					t.Fatalf("cancel blocked field update=%t error=%v", canceled, err)
				}
				outcome := join()
				if outcome.Result != nil {
					t.Fatal("canceled field update exposed result")
				}
				want := fixture.CompositeReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, outcome.Err, &want)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
			}()
		}
	}
	compositeExprBoth(t, p, b, c, `SELECT v FROM holder ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(7,z)"}, {"(8,y)"}})
}
