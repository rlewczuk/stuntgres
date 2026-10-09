package reference_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceUpdateReturningCapturedRegclass(t *testing.T) {
	updateReturningRegclassContextReplay(t)
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(2)")
		rows, err := db.Query("UPDATE t SET id=id RETURNING 't'::regclass AS target")
		if err != nil {
			t.Fatal(err)
		}
		updateReturningReferenceExec(t, db, "ALTER TABLE t RENAME TO renamed")
		if !backend.pg {
			if err = stuntgres.Reset(context.Background(), p.native); err != nil {
				rows.Close()
				t.Fatal(err)
			}
		}
		deleteReferenceConsume(t, backend.pg, rows, nil, referenceStep{columns: []string{"target"}, types: []string{"REGCLASS"}, rows: [][]any{{"t"}, {"t"}}, bag: true})
	}
}

func updateReturningRegclassContextReplay(t *testing.T) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			rows, err := backend.db.QueryContext(p.ctx, "UPDATE t SET id=id RETURNING 't'::regclass AS target")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
			if !backend.pg {
				if err = stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			}
			updateCoreRetained(t, backend.pg, rows, []string{"target"}, []string{"REGCLASS"}, [][]any{{"t"}, {"t"}}, true)
		})
	}
}
