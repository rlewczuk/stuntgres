package reference_test

import (
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningCapturedRegclass(t *testing.T) {
	updateReturningRegclassContextReplay(t)
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(2)")
		rows, err := db.Query("UPDATE t SET id=id RETURNING 't'::regclass AS target")
		if err != nil {
			t.Fatal(err)
		}
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE t RENAME TO renamed")
		if !backend.PG {
			if err = stuntgres.Reset(context.Background(), p.Native); err != nil {
				rows.Close()
				t.Fatal(err)
			}
		}
		fixture.DeleteReferenceConsume(t, backend.PG, rows, nil, fixture.Step{Columns: []string{"target"}, Types: []string{"REGCLASS"}, Rows: [][]any{{"t"}, {"t"}}, Bag: true})
	}
}

func updateReturningRegclassContextReplay(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			rows, err := backend.DB.QueryContext(p.Ctx, "UPDATE t SET id=id RETURNING 't'::regclass AS target")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			run(fixture.Exec("ALTER TABLE t RENAME TO renamed", 0))
			if !backend.PG {
				if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			fixture.UpdateCoreRetained(t, backend.PG, rows, []string{"target"}, []string{"REGCLASS"}, [][]any{{"t"}, {"t"}}, true)
		})
	}
}
