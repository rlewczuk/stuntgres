package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryBasicTruthfulCapabilityBoundaries(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA,s TEXT)", 0))
			for _, backend := range p.Backends() {
				for _, tc := range []struct {
					q, typ string
					value  any
				}{
					{"SELECT length(NULL)", "INT4", nil},
					{"SELECT octet_length('é')", "INT4", int64(2)},
					{"SELECT bit_length($1)", "INT4", int64(8)},
					{"SELECT length(v,'UTF8') FROM t", "INT4", nil},
				} {
					if tc.q != "SELECT length(v,'UTF8') FROM t" || backend.PG {
						q := tc.q
						name := "length"
						if q == "SELECT octet_length('é')" {
							name = "octet_length"
						}
						if q == "SELECT bit_length($1)" {
							name = "bit_length"
						}
						var args []any
						if name == "bit_length" {
							args = []any{"A"}
						}
						var expected [][]any
						if q != "SELECT length(v,'UTF8') FROM t" {
							expected = [][]any{{tc.value}}
						}
						s := fixture.Select(q, []string{name}, []string{tc.typ}, expected, args...)
						run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
					} else {
						var stmt *sql.Stmt
						var err error
						if nonContext {
							stmt, err = backend.DB.Prepare(tc.q)
						} else {
							stmt, err = backend.DB.PrepareContext(p.Ctx, tc.q)
						}
						if stmt != nil {
							stmt.Close()
							t.Fatal("published unsupported statement", tc.q)
						}
						fixture.State(t, err, "0A000")
					}
				}
			}

		})
	}
}

func TestReferenceBinaryBasicExcludedSyntaxAndOccurrenceTyping(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA)", 0))
			apply(t, fixture.Exec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, fixture.Exec(`INSERT INTO t VALUES('\x00ff80')`, 1))
			apply(t, fixture.Select("SELECT v::bytea,length(v::bytea) AS n FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"\x00\xff\x80", int64(3)}}))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT TRIM(v) FROM t", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.btrim(bytea) does not exist", Position: "8", Hint: hint}},
				{"INSERT INTO dst SELECT 'a'||'b' FROM t", fixture.Diagnostic{Code: "42804", Message: `column "v" is of type bytea but expression is of type text`, Position: "24", Hint: "You will need to rewrite or cast the expression."}},
				{"SELECT v FROM t LIMIT length(v)", fixture.Diagnostic{Code: "42P10", Message: "argument of LIMIT must not contain variables", Position: "30"}},
			} {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.ErrorStage = "execute"
				apply(t, s)
				apply(t, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
				s.ErrorStage = "prepare"
				apply(t, fixture.Prepared(s))
				apply(t, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
			}
			apply(t, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
			for _, backend := range p.Backends() {
				run(t, p.Ctx, backend.DB, backend.Recorder, fixture.ImplicitError("CREATE TABLE bad(v BYTEA DEFAULT length(NULL))", fixture.Diagnostic{Code: "42804", Message: `column "v" is of type bytea but default expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}))
				if backend.PG {
					run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SELECT byteacat(v,v) FROM t", 1))
					run(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT byteacat(v,v) FROM t", []string{"byteacat"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80\x00\xff\x80"}}))
				} else {
					for _, q := range []string{"SELECT byteacat(v,v) FROM t"} {
						var err error
						if nonContext {
							_, err = backend.DB.Exec(q)
						} else {
							_, err = backend.DB.ExecContext(p.Ctx, q)
						}
						fixture.State(t, err, "0A000")
					}
				}
				var stmt *sql.Stmt
				var err error
				if nonContext {
					stmt, err = backend.DB.Prepare("SELECT $1,v||$1 FROM t")
				} else {
					stmt, err = backend.DB.PrepareContext(p.Ctx, "SELECT $1,v||$1 FROM t")
				}
				if stmt != nil {
					stmt.Close()
					t.Fatal("incompatible occurrence published statement")
				}
				fixture.State(t, err, "42P08")
			}
		})
	}
}
