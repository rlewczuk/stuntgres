package reference_test

import "testing"

func TestReferenceDeleteCoreCastPreflightOverflow(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			for _, populated := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					var ids [][]any
					if populated {
						run(referenceExec("INSERT INTO t VALUES(1)", 1))
						ids = [][]any{{int64(1)}}
					}
					step := referenceImplicitError("DELETE FROM t WHERE id=CAST($1::int8 AS int4)", referenceDiagnostic{Code: "22003", Message: "integer out of range"})
					step.args, step.prepared, step.errorStage = []any{int64(2147483648)}, prepared, "execute"
					deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
					run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, ids))
					run(referenceExec("DROP TABLE t", 0))
				}
			}

		})
	}
}

func TestReferenceDeleteCoreCastPreflightRows(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
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
						if !backend.pg && want.Code == "" {
							// Native logical evaluation does not prune row casts.
							want = referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
						}
						if populated && backend.pg && want.Code == "" {
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
						deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, step)
						run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
						run(referenceExec("DROP TABLE t", 0))
					}
				}
			}

		})
	}
}

func TestReferenceDeleteCoreCastPreflightAbort(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			run(referenceExec("CREATE TABLE t(id INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'42'),(2,'oops')", 2))
			db := backend.db
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO t VALUES(7,'7')", 1))
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("DELETE FROM t WHERE id=1", 1))
			failure := referenceImplicitError("DELETE FROM t WHERE int4(s) IS NOT NULL", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			failure.errorStage = "execute"
			deleteReferenceExecuteStep(t, p, tx, backend.recorder, failure)
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
