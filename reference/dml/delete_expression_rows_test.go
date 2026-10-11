package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreCastPreflightOverflow(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			for _, populated := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					var ids [][]any
					if populated {
						run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						ids = [][]any{{int64(1)}}
					}
					step := fixture.ImplicitError("DELETE FROM t WHERE id=CAST($1::int8 AS int4)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
					step.Args, step.Prepared, step.ErrorStage = []any{int64(2147483648)}, prepared, "execute"
					fixture.DeleteReferenceExecuteStep(t, p, backend.DB, backend.Recorder, step)
					run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, ids))
					run(fixture.Exec("DROP TABLE t", 0))
				}
			}

		})
	}
}

func TestReferenceDeleteCoreCastPreflightRows(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			for _, tc := range []struct {
				predicate string
				pgCount   int64
				want      fixture.Diagnostic
			}{
				{"FALSE AND int4(s)=42", 0, fixture.Diagnostic{}},
				{"TRUE OR int4(s)=42", 2, fixture.Diagnostic{}},
				{"int4(s) IS DISTINCT FROM 0", 0, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"CAST(n AS int4) IS NOT NULL", 0, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
			} {
				for _, populated := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						run(fixture.Exec("CREATE TABLE t(id INT,s TEXT,n INT8)", 0))
						var ids [][]any
						if populated {
							run(fixture.Exec("INSERT INTO t VALUES(1,'42',42),(2,'oops',2147483648)", 2))
							ids = [][]any{{int64(1)}, {int64(2)}}
						}
						count, want := int64(0), tc.want
						if !backend.PG && want.Code == "" {
							// Native logical evaluation does not prune row casts.
							want = fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
						}
						if populated && backend.PG && want.Code == "" {
							count = tc.pgCount
							if count == 2 {
								ids = nil
							}
						}
						step := fixture.Exec("DELETE FROM t WHERE "+tc.predicate, count)
						if populated && want.Code != "" {
							step = fixture.ImplicitError(step.Query, want)
							step.ErrorStage = "execute"
						}
						step.Prepared = prepared
						fixture.DeleteReferenceExecuteStep(t, p, backend.DB, backend.Recorder, step)
						run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
						run(fixture.Exec("DROP TABLE t", 0))
					}
				}
			}

		})
	}
}

func TestReferenceDeleteCoreCastPreflightAbort(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			run(fixture.Exec("CREATE TABLE t(id INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'42'),(2,'oops')", 2))
			db := backend.DB
			tx, err := db.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(7,'7')", 1))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("DELETE FROM t WHERE id=1", 1))
			failure := fixture.ImplicitError("DELETE FROM t WHERE int4(s) IS NOT NULL", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			failure.ErrorStage = "execute"
			fixture.DeleteReferenceExecuteStep(t, p, tx, backend.Recorder, failure)
			aborted := fixture.ImplicitError("SELECT id FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			aborted.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, aborted)
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}
