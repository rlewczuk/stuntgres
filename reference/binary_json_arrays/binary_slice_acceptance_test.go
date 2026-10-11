package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceHeterogeneousNullAndOptionalCounts(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			for _, tc := range []struct {
				expression string
				want       any
			}{
				{"overlay(NULL::bytea,'X'::bytea,0,1)", nil},
				{"overlay('abc'::bytea,NULL::bytea,0,1)", nil},
				{"overlay('abc'::bytea,'X'::bytea,NULL::integer,1)", nil},
				{"overlay('abc'::bytea,'X'::bytea,0,NULL::integer)", nil},
				{"substring(NULL::bytea,1,-1)", nil},
				{"substring('abc'::bytea,NULL::integer,-1)", nil},
				{"substring('abc'::bytea,1,NULL::integer)", nil},
				{"substr(NULL::bytea,1,-1)", nil},
				{"substr('abc'::bytea,NULL::integer,-1)", nil},
				{"substr('abc'::bytea,1,NULL::integer)", nil},
				{`overlay('abcdef'::bytea PLACING '\xc3a9'::bytea FROM 3)`, "ab\xc3\xa9ef"},
				{"overlay('abcdef'::bytea,''::bytea,3)", "abcdef"},
				{"overlay('abcdef'::bytea,''::bytea,2147483647)", "abcdef"},
				{"overlay('abcdef'::bytea,'X'::bytea,2147483647,0)", "abcdefX"},
			} {
				step := fixture.Select("SELECT "+tc.expression+" AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}})
				apply(t, step)
				apply(t, fixture.Prepared(step))
				for _, backend := range p.Backends() {
					stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT "+tc.expression)
					if err != nil {
						t.Fatal(err)
					}
					var got any
					err = stmt.QueryRowContext(p.Ctx).Scan(&got)
					if closeErr := stmt.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err == nil {
						fixture.AssertRawValue(t, backend.PG, "BYTEA", got)
					}
					if err != nil || !reflect.DeepEqual(fixture.Normalize(got), tc.want) {
						t.Fatal(tc.expression, got, tc.want, err)
					}
					original, err := backend.DB.Prepare("SELECT " + tc.expression)
					if err != nil {
						t.Fatal(err)
					}
					err = original.QueryRow().Scan(&got)
					if closeErr := original.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					fixture.AssertRawValue(t, backend.PG, "BYTEA", got)
					if !reflect.DeepEqual(fixture.Normalize(got), tc.want) {
						t.Fatal(tc.expression, got, tc.want)
					}
				}
			}
			for _, q := range []string{"SELECT overlay('abcdef'::bytea,'X'::bytea,2147483647)", "SELECT overlay('abcdef'::bytea,''::bytea,2147483647,1)"} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				apply(t, step)
				apply(t, fixture.Prepared(step))
			}
		})
	}
}

func TestReferenceBinarySliceForOnlyInputPhase(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA)", 0))
			for _, populated := range []bool{false, true} {
				if populated {
					apply(t, fixture.Exec("INSERT INTO t VALUES(NULL),('abcdef')", 2))
				}
				step := fixture.ImplicitError("SELECT substring(v FOR '1.5') FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "1.5"`, Position: "24"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				apply(t, step)
				step.ErrorStage = "prepare"
				apply(t, fixture.Prepared(step))
			}
		})
	}
}

func TestReferenceBinarySliceLateOverlayAtomicity(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			apply(t, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
			apply(t, fixture.Exec("INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2147483647,1)", 2))
			apply(t, fixture.Exec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, fixture.Exec("INSERT INTO dst VALUES('sentinel')", 1))
			for _, q := range []string{"INSERT INTO dst SELECT overlay(v,s,i,n) FROM t ORDER BY id", "UPDATE t SET v=overlay(v PLACING s FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING overlay(v,s,i,n)"} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				apply(t, step)
				apply(t, fixture.Prepared(step))
				apply(t, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"sentinel"}}))
				apply(t, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "abcdef"}, {int64(2), "abcdef"}}))
			}
		})
	}
}

func TestReferenceBinarySliceOrdinaryPositionDeparse(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, tc := range []struct{ name, callee string }{{"quoted", `"position"`}, {"qualified", "pg_catalog.position"}} {
				apply(t, fixture.Exec("CREATE TABLE p_"+tc.name+"(v BYTEA,s BYTEA,CONSTRAINT ck_"+tc.name+" CHECK("+tc.callee+"(v,s)>0))", 0))
				for _, mode := range []struct{ flag, want string }{{"false", `("position"(v, s) > 0)`}, {"true", `"position"(v, s) > 0`}} {
					expr := fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
					def := fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
					apply(t, expr)
					apply(t, fixture.Prepared(expr))
					apply(t, def)
					apply(t, fixture.Prepared(def))
				}
				apply(t, fixture.Exec("INSERT INTO p_"+tc.name+" VALUES('abcdef','cd')", 1))
				step := fixture.Select("SELECT "+tc.callee+"(v,s) AS p FROM p_"+tc.name+" WHERE "+tc.callee+"(v,s)>0", []string{"p"}, []string{"INT4"}, [][]any{{int64(3)}})
				apply(t, step)
				apply(t, fixture.Prepared(step))
			}

			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"quoted", "qualified"} {
				run(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT pg_catalog.position(v,s) FROM p_"+name, []string{"position"}, []string{"INT4"}, [][]any{{int64(3)}}))
				_, err := p.Native.Exec("INSERT INTO p_" + name + " VALUES('abcdef','zz')")
				fixture.State(t, err, "23514")
			}
		})
	}
}
