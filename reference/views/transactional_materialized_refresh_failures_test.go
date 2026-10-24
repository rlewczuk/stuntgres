package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshFailures(t *testing.T) {
	for _, failure := range []string{"producer", "identical", "key_only"} {
		for _, commit := range []bool{false, true} {
			t.Run(failure+"/"+map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
				txRefreshRoutes(t, []string{"sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
					base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					base(fixture.Exec("CREATE TABLE txr_source(a INT,b INT)", 0))
					base(fixture.Exec("INSERT INTO txr_source VALUES(1,10)", 1))
					projection := "a,b"
					if failure == "producer" {
						projection = "a,100/b AS b"
					}
					base(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT "+projection+" FROM txr_source", 1))
					base(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a)", 0))
					q := "REFRESH MATERIALIZED VIEW mv"
					if concurrent {
						q = "REFRESH MATERIALIZED VIEW CONCURRENTLY mv"
					}
					route := txRefreshHeld(t, p, c, held, q)
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					run(fixture.Exec("INSERT INTO txr_source VALUES(2,20)", 1))
					fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
					run(fixture.Exec("SAVEPOINT good", 0))
					insert := "(1,20)"
					if failure == "identical" {
						insert = "(1,10)"
					}
					if failure == "producer" {
						insert = "(3,0)"
					}
					run(fixture.Exec("INSERT INTO txr_source VALUES"+insert, 1))
					d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "mv_u"`)
					d.Detail = "Key (a)=(1) already exists."
					d.Schema = "public"
					d.Table = "mv"
					d.Constraint = "mv_u"
					if failure == "producer" {
						d = fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					} else if concurrent && failure == "identical" {
						d = fixture.ControlsReferenceDiagnostic("21000", `new data for materialized view "mv" contains duplicate rows without any null columns`)
						d.Detail = "Row: (1,10)"
					} else if !concurrent {
						d.Message = `could not create unique index "mv_u"`
						d.Detail = "Key (a)=(1) is duplicated."
					}
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					var executor fixture.Executor = route(e)
					if concurrent && d.Code == "23505" {
						executor = concurrentRuntimeExecutor{executor, t, b.PG}
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
					run(fixture.Exec("ROLLBACK TO good", 0))
					want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
					if failure == "producer" {
						want = [][]any{{int64(1), int64(10)}, {int64(2), int64(5)}}
					}
					read := fixture.Select("SELECT a,b FROM mv ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, want)
					run(read)
					fixture.MaterializedViewPopulation(run, "mv", true)
					run(fixture.Select("SELECT a,b FROM txr_source ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
					run(read)
					run(fixture.Exec("RELEASE good", 0))
					finish(commit)
					if commit {
						base(read)
					} else {
						base(fixture.Select("SELECT a,b FROM mv", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
					}
					n := int64(1)
					if commit {
						n = 2
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) AS n FROM txr_source", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
					base(fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
			})
		}
	}
}
