package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateArrayCardinalityPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, source, set, code, message string }{
		{"nullSecondIndex", "(1,1),(2,NULL::integer)", "a[s.i]=7", "22004", "array subscript in assignment must not be null"},
		{"shortSecondSlice", "(1,2),(2,1)", "a[1:2]=CASE WHEN s.i=2 THEN ARRAY[7,8] ELSE ARRAY[9] END", "2202E", "source array too small"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT[])", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,ARRAY[10,20,30])", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,i INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES"+tc.source, 2))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON TRUE WHEN MATCHED THEN UPDATE SET "+tc.set, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.ArrayReferenceSelect("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{10,20,30}"}}, b.PG))
			})
		})
	}
}

func TestReferenceMergeUpdateUniquePrefixes(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source, arms string
		fail                     bool
		key                      int64
		rows                     [][]any
	}{
		{"swapNotDeferred", "(1,10),(2,20)", "(1,1,2),(2,2,1)", "WHEN MATCHED THEN UPDATE SET id=s.newid", true, 2, nil},
		{"removeOldKeyThenInsert", "(1,10)", "(1,1,3),(2,2,1)", "WHEN MATCHED THEN UPDATE SET id=s.newid WHEN NOT MATCHED THEN INSERT VALUES(s.newid,99)", false, 0, [][]any{{int64(1), int64(99)}, {int64(3), int64(10)}}},
		{"insertThenUpdateConflict", "(1,10)", "(1,2,2),(2,1,2)", "WHEN MATCHED THEN UPDATE SET id=s.newid WHEN NOT MATCHED THEN INSERT VALUES(s.newid,99)", true, 2, nil},
		{"crossUpdateArms", "(1,10),(2,20)", "(1,1,3),(2,2,3)", "WHEN MATCHED AND s.id=1 THEN UPDATE SET id=3 WHEN MATCHED THEN UPDATE SET id=3", true, 3, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_key UNIQUE,v INT)", 0))
				n := int64(1)
				if strings.Count(tc.seed, "),(") > 0 {
					n = 2
				}
				run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, n))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT,newid INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES"+tc.source, 2))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id=s.id " + tc.arms
				want := tc.rows
				if tc.fail {
					d := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_key"`, Schema: "public", Table: "t", Constraint: "t_key"}
					if tc.key == 2 {
						d.Detail = "Key (id)=(2) already exists."
					} else {
						d.Detail = "Key (id)=(3) already exists."
					}
					s := fixture.ImplicitError(q, d)
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}}
					if n == 2 {
						want = append(want, []any{int64(2), int64(20)})
					}
				} else {
					run(fixture.Exec(q, 2))
				}
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceMergeUpdatePreflightOrder(t *testing.T) {
	mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "GRANT SELECT ON source TO merge_update_actor"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_actor", 0)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "MERGE INTO t USING source s ON TRUE WHEN MATCHED AND FALSE THEN UPDATE SET v=$1::integer"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, bad := range []bool{false, true} {
			fixture.LockExec(t, p.Ctx, c, "BEGIN READ ONLY", 0)
			arg := any(1)
			if bad {
				arg = "bad"
			}
			var r interface{ RowsAffected() (int64, error) }
			if prepared {
				r, err = held.ExecContext(p.Ctx, arg)
			} else {
				r, err = c.ExecContext(p.Ctx, q, arg)
			}
			if r != nil {
				t.Fatal("multifault command exposed result")
			}
			d := fixture.ControlsReferenceDiagnostic("25006", "cannot execute MERGE in a read-only transaction")
			if bad {
				d = fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				d.Context = "unnamed portal parameter $1 = '...'"
			}
			fixture.AssertDiagnostic(t, err, &d)
			fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
		}
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	})
}

func TestReferenceMergeUpdateSystemColumns(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND t.ctid IS NOT NULL THEN UPDATE SET v=20"
		d := fixture.Diagnostic{Code: "42P10", Message: `cannot use system column "ctid" in MERGE WHEN condition`, Position: fixture.Position(strings.Index(q, "t.ctid") + 1)}
		s := fixture.ImplicitError(q, d)
		s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
		run(s)
		run(fixture.Exec("MERGE INTO t USING (VALUES(1,TRUE)) s(id,ctid) ON t.id=s.id WHEN MATCHED AND s.ctid THEN UPDATE SET v=20", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}}))
	})
}
