//go:build reference

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
					p := newReferencePair(t)
					for _, backend := range []struct {
						name     string
						db       referenceExecutor
						recorder *referenceRecorder
					}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
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
							run(step)
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
					p := newReferencePair(t)
					for _, backend := range []struct {
						name     string
						db       referenceExecutor
						recorder *referenceRecorder
					}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
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
							if backend.name == "postgres" && predicate != "int4($1::text)=id" {
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
							run(step)
							run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, survivors))
						})
					}
				})
			}
		}
	}
}

func TestReferenceDeleteCoreExpressionDiagnosticBoundaries(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       referenceExecutor
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
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
					run(step)
				}
			}
			step := referenceImplicitError("DELETE FROM t WHERE $1 IS NULL", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
			step.prepared, step.errorStage = true, "prepare"
			run(step)
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
	t.Run("oracle_qualified_cast_capability", func(t *testing.T) {
		p := newReferencePair(t)
		for _, query := range []string{"DELETE FROM t AS d WHERE d.v::int4=10", "DELETE FROM t AS d WHERE CAST(d.v AS int4)=10", "DELETE FROM t AS d WHERE int4(d.v)=10"} {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO t VALUES(1,10),(2,20),(4,10),(4,10)", 4))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(query, 3))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DROP TABLE t", 0))
		}
	})
}

func TestReferenceDeleteCoreCastPreflightReuseAndRows(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       referenceExecutor
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
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
			run(referenceExec("DROP TABLE t", 0))
			for _, populated := range []bool{false, true} {
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				var ids [][]any
				if populated {
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					ids = [][]any{{int64(1)}}
				}
				step := referenceImplicitError("DELETE FROM t WHERE id=CAST($1::int8 AS int4)", referenceDiagnostic{Code: "22003", Message: "integer out of range"})
				step.args, step.prepared, step.errorStage = []any{int64(2147483648)}, true, "execute"
				run(step)
				run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, ids))
				run(referenceExec("DROP TABLE t", 0))
			}
			for _, tc := range []struct {
				predicate string
				pgCount   int64
				want      referenceDiagnostic
			}{
				{"FALSE AND int4(s)=42", 0, referenceDiagnostic{}},
				{"TRUE OR int4(s)=42", 2, referenceDiagnostic{}},
				{"int4(s) IS DISTINCT FROM 0", 0, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"CAST(n AS int4) IS NOT NULL", 0, referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
			} {
				for _, populated := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						run(referenceExec("CREATE TABLE t(id INT,s TEXT,n INT8)", 0))
						var ids [][]any
						if populated {
							run(referenceExec("INSERT INTO t VALUES(1,'42',42),(2,'oops',2147483648)", 2))
							ids = [][]any{{int64(1)}, {int64(2)}}
						}
						count, want := int64(0), tc.want
						if backend.name == "native" && want.Code == "" {
							// Native logical evaluation does not prune row casts.
							want = referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
						}
						if populated && backend.name == "postgres" && want.Code == "" {
							count = tc.pgCount
							if count == 2 {
								ids = nil
							}
						}
						step := referenceExec("DELETE FROM t WHERE "+tc.predicate, count)
						if populated && want.Code != "" {
							step = referenceImplicitError(step.query, want)
							step.errorStage = "execute"
						}
						step.prepared = prepared
						run(step)
						run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
						run(referenceExec("DROP TABLE t", 0))
					}
				}
			}
			run(referenceExec("CREATE TABLE t(id INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'42'),(2,'oops')", 2))
			db := p.postgres
			if backend.name == "native" {
				db = p.memory
			}
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO t VALUES(7,'7')", 1))
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("DELETE FROM t WHERE id=1", 1))
			failure := referenceImplicitError("DELETE FROM t WHERE int4(s) IS NOT NULL", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, backend.recorder, failure)
			aborted := referenceImplicitError("SELECT id FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			aborted.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, backend.recorder, aborted)
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferenceDeleteCoreExpressionACLPrecedence(t *testing.T) {
	const role = "delete_expression_actor"
	roleReferenceCleanup(t, []string{role})
	p := newReferencePair(t)
	admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
	t.Cleanup(func() {
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("DROP SCHEMA IF EXISTS hidden CASCADE", 0))
	})
	for _, query := range []string{"CREATE USER " + role, "CREATE TABLE t(i INT,s TEXT)", "INSERT INTO t VALUES(1,'oops')", "GRANT USAGE ON SCHEMA public TO " + role, "CREATE SCHEMA hidden", "CREATE TABLE hidden.t(i INT)"} {
		count := int64(0)
		if query == "INSERT INTO t VALUES(1,'oops')" {
			count = 1
		}
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(query, count))
	}
	referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+role, 0))
	t.Cleanup(func() {
		referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
	for _, tc := range []struct {
		query string
		args  []any
		want  referenceDiagnostic
	}{
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"oops"}, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"1"}, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM t WHERE int4(s) IS NULL", nil, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM hidden.t WHERE i=int4($1::text)", []any{"oops"}, referenceDiagnostic{Code: "42501", Message: "permission denied for schema hidden", Position: "13"}},
	} {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError(tc.query, tc.want, tc.args...)
			step.prepared, step.errorStage = prepared, "execute"
			if prepared && tc.want.Message == "permission denied for schema hidden" {
				step.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, actor, p.postgresRecord, step)
			referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
