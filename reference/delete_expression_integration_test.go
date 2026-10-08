package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceDeleteCoreExpressionIntegration(t *testing.T) {
	for _, tc := range []struct {
		name, predicate string
		count           int64
		survivors       [][]any
	}{
		{"null", "d.v IS NULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"not_null", "d.v IS NOT NULL", 4, [][]any{{int64(2)}}},
		{"isnull", "d.v ISNULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"notnull", "d.v NOTNULL", 4, [][]any{{int64(2)}}},
		{"true", "d.f IS TRUE", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"not_true", "d.f IS NOT TRUE", 2, [][]any{{int64(1)}, {int64(4)}, {int64(4)}}},
		{"false", "d.f IS FALSE", 1, [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(4)}}},
		{"not_false", "d.f IS NOT FALSE", 4, [][]any{{int64(3)}}},
		{"unknown", "d.f IS UNKNOWN", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"not_unknown", "d.f IS NOT UNKNOWN", 4, [][]any{{int64(2)}}},
		{"distinct", "d.v IS DISTINCT FROM 10", 2, [][]any{{int64(1)}, {int64(4)}, {int64(4)}}},
		{"not_distinct", "d.v IS NOT DISTINCT FROM 10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"distinct_null", "d.v IS DISTINCT FROM NULL", 4, [][]any{{int64(2)}}},
		{"not_distinct_null", "d.v IS NOT DISTINCT FROM NULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"cast", "CAST(v AS BIGINT)=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"postfix", "v::BIGINT=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"function", "int8(v)=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"comparison_tier", "d.v=10 IS TRUE", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"logical_tiers", "NOT d.v IS NULL AND d.f IS TRUE OR d.v=20", 4, [][]any{{int64(2)}}},
		{"unary_chain", "TRUE IS NULL IS TRUE", 0, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"grouped_distinct", "(d.f IS TRUE) IS DISTINCT FROM (d.v IS NULL OR d.f IS FALSE)", 5, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							run := func(step referenceStep) {
								t.Helper()
								referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
							}
							run(referenceExec("CREATE TABLE t(id INT,v INT,f BOOL)", 0))
							run(referenceExec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5))
							step := referenceExec("DELETE FROM t AS d WHERE "+tc.predicate, tc.count)
							if prepared {
								step = referenceExec("DELETE FROM t d WHERE "+tc.predicate, tc.count)
							}
							if prepared {
								step = referencePrepared(step)
							}
							deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
							run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, tc.survivors))
						})
					}
				})
			}
		})
	}
}

func TestReferenceDeleteCoreCastPreflight(t *testing.T) {
	for _, predicate := range []string{"int4($1::text)=id", "FALSE AND int4($1::text)=id", "TRUE OR int4($1::text)=id"} {
		for _, populated := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%t", predicate, populated, prepared), func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							run := func(step referenceStep) {
								t.Helper()
								referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
							}
							run(referenceExec("CREATE TABLE t(id INT)", 0))
							var survivors [][]any
							if populated {
								run(referenceExec("INSERT INTO t VALUES(7)", 1))
								survivors = [][]any{{int64(7)}}
							}
							step := referenceImplicitError("DELETE FROM t WHERE "+predicate, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
							// PostgreSQL simplifies decisive branches; native preflight
							// deliberately visits every frame-only conversion.
							if backend.pg && predicate != "int4($1::text)=id" {
								count := int64(0)
								if populated && predicate == "TRUE OR int4($1::text)=id" {
									count = 1
									survivors = nil
								}
								step = referenceExec("DELETE FROM t WHERE "+predicate, count)
							}
							step.args = []any{"bad"}
							step.prepared = prepared
							if step.diagnostic != nil {
								step.errorStage = "execute"
							}
							deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
							run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, survivors))
						})
					}
				})
			}
		}
	}
}

func TestReferenceDeleteCoreExpressionDiagnosticBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
			run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
			for _, tc := range []struct {
				query string
				want  referenceDiagnostic
			}{
				{"DELETE FROM t WHERE v IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "21"}},
				{"DELETE FROM t WHERE int8(TRUE) IS NULL", referenceDiagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "21", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
			} {
				for _, prepared := range []bool{false, true} {
					step := referenceImplicitError(tc.query, tc.want)
					step.prepared = prepared
					step.errorStage = "execute"
					if prepared {
						step.errorStage = "prepare"
					}
					deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
				}
			}
			step := referenceImplicitError("DELETE FROM t WHERE $1 IS NULL", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
			step.prepared, step.errorStage = true, "prepare"
			deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferenceDeleteCoreCastPreflightReuseAndRows(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
			run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,10),(2,NULL),(3,20),(4,10),(4,10)", 5))
			stmt, err := backend.db.PrepareContext(p.ctx, "DELETE FROM t WHERE v IS NOT DISTINCT FROM int4($1::text)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, call := range []struct {
				arg   any
				count int64
				bad   bool
				ids   [][]any
			}{
				{"oops", 0, true, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(4)}}},
				{"10", 3, false, [][]any{{int64(2)}, {int64(3)}}},
				{nil, 1, false, [][]any{{int64(3)}}},
				{"oops", 0, true, [][]any{{int64(3)}}},
				{"20", 1, false, nil},
			} {
				result, err := stmt.ExecContext(p.ctx, call.arg)
				if call.bad {
					if result != nil {
						t.Fatal(result)
					}
					referenceCheckDiagnostic(t, err, referenceImplicitError("", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}), "execute")
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if count, err := result.RowsAffected(); err != nil || count != call.count {
						t.Fatal(count, err)
					}
				}
				run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, call.ids))
			}

		})
	}
}
