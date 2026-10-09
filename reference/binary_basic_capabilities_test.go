package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceBinaryBasicTruthfulCapabilityBoundaries(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			apply(t, referenceExec("CREATE TABLE t(v BYTEA,s TEXT)", 0))
			for _, backend := range p.backends() {
				for _, tc := range []struct {
					q, typ string
					value  any
				}{
					{"SELECT length(NULL)", "INT4", nil},
					{"SELECT octet_length('é')", "INT4", int64(2)},
					{"SELECT bit_length($1)", "INT4", int64(8)},
					{"SELECT length(v,'UTF8') FROM t", "INT4", nil},
				} {
					if tc.q != "SELECT length(v,'UTF8') FROM t" || backend.pg {
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
						s := referenceSelect(q, []string{name}, []string{tc.typ}, expected, args...)
						run(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
					} else {
						var stmt *sql.Stmt
						var err error
						if nonContext {
							stmt, err = backend.db.Prepare(tc.q)
						} else {
							stmt, err = backend.db.PrepareContext(p.ctx, tc.q)
						}
						if stmt != nil {
							stmt.Close()
							t.Fatal("published unsupported statement", tc.q)
						}
						referenceState(t, err, "0A000")
					}
				}
			}

		})
	}
}

func TestReferenceBinaryBasicExcludedSyntaxAndOccurrenceTyping(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			apply(t, referenceExec("CREATE TABLE t(v BYTEA)", 0))
			apply(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, referenceExec(`INSERT INTO t VALUES('\x00ff80')`, 1))
			apply(t, referenceSelect("SELECT v::bytea,length(v::bytea) AS n FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"\x00\xff\x80", int64(3)}}))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want referenceDiagnostic
			}{
				{"SELECT TRIM(v) FROM t", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.btrim(bytea) does not exist", Position: "8", Hint: hint}},
				{"INSERT INTO dst SELECT 'a'||'b' FROM t", referenceDiagnostic{Code: "42804", Message: `column "v" is of type bytea but expression is of type text`, Position: "24", Hint: "You will need to rewrite or cast the expression."}},
				{"SELECT v FROM t LIMIT length(v)", referenceDiagnostic{Code: "42P10", Message: "argument of LIMIT must not contain variables", Position: "30"}},
			} {
				s := referenceImplicitError(tc.q, tc.want)
				s.errorStage = "execute"
				apply(t, s)
				apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
				s.errorStage = "prepare"
				apply(t, referencePrepared(s))
				apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
			}
			apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
			for _, backend := range p.backends() {
				run(t, p.ctx, backend.db, backend.recorder, referenceImplicitError("CREATE TABLE bad(v BYTEA DEFAULT length(NULL))", referenceDiagnostic{Code: "42804", Message: `column "v" is of type bytea but default expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}))
				if backend.pg {
					run(t, p.ctx, backend.db, backend.recorder, referenceExec("SELECT byteacat(v,v) FROM t", 1))
					run(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT byteacat(v,v) FROM t", []string{"byteacat"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80\x00\xff\x80"}}))
				} else {
					for _, q := range []string{"SELECT byteacat(v,v) FROM t"} {
						var err error
						if nonContext {
							_, err = backend.db.Exec(q)
						} else {
							_, err = backend.db.ExecContext(p.ctx, q)
						}
						referenceState(t, err, "0A000")
					}
				}
				var stmt *sql.Stmt
				var err error
				if nonContext {
					stmt, err = backend.db.Prepare("SELECT $1,v||$1 FROM t")
				} else {
					stmt, err = backend.db.PrepareContext(p.ctx, "SELECT $1,v||$1 FROM t")
				}
				if stmt != nil {
					stmt.Close()
					t.Fatal("incompatible occurrence published statement")
				}
				referenceState(t, err, "42P08")
			}
		})
	}
}
