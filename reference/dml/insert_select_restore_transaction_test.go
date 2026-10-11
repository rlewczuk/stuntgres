package reference_test

import (
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectFailedRestoreTransactionEligibility(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(i INT)", 0), fixture.Exec("INSERT INTO t VALUES(9)", 1), fixture.Exec("CREATE TABLE s(i INT)", 0), fixture.Exec("INSERT INTO s VALUES(1)", 1)} {
				run(s)
			}
			tx, err := backend.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.InsertSelectRun(t, ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(7)", 1))
			query := "INSERT INTO t SELECT i FROM s"
			producer, err := tx.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close()
			if !backend.PG {
				err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(i INT); INSERT INTO t SELECT $1"))
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
			}
			fixture.InsertSelectRun(t, ctx, fixture.HeldStatement{tx, producer, query}, backend.Recorder, fixture.Exec(query, 1))
			run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}, {int64(9)}}))
		})
	}
}
