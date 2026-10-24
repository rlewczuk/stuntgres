package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateTransactions(t *testing.T) {
	for _, form := range []struct{ query, tag string }{
		{"CREATE TABLE qc_tx AS SELECT 7 AS i", "CREATE TABLE AS"},
		{"SELECT 7 AS i INTO qc_tx", "SELECT INTO"},
	} {
		for _, route := range []string{"direct", "prepare", "held"} {
			t.Run(form.tag+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, run := fixture.QueryTableCreateSession(t, p, b)
						var held *sql.Stmt
						if route == "held" {
							var err error
							held, err = conn.PrepareContext(p.Ctx, form.query)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
						}
						tx, err := conn.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						executor := fixture.Executor(tx)
						if held != nil {
							stmt := tx.StmtContext(p.Ctx, held)
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: form.query}
						}
						s := fixture.Exec(form.query, 1)
						s.Prepared = route == "prepare"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT i FROM qc_tx", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.QueryTableCreateAbsent(t, run, "qc_tx")
						run(fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}

func TestReferenceQueryTableCreateReadOnly(t *testing.T) {
	for _, form := range []struct{ query, tag string }{
		{"CREATE TABLE qc_ro AS SELECT i FROM qc_ro_source", "CREATE TABLE AS"},
		{"SELECT i INTO qc_ro FROM qc_ro_source", "SELECT INTO"},
	} {
		for _, missing := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(form.tag+map[bool]string{false: "/valid", true: "/missing"}[missing]+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							conn, run := fixture.QueryTableCreateSession(t, p, b)
							run(fixture.Exec("CREATE TABLE qc_ro_source(i INT)", 0))
							var stmt *sql.Stmt
							if held {
								var err error
								stmt, err = conn.PrepareContext(p.Ctx, form.query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
							}
							if missing {
								run(fixture.Exec("DROP TABLE qc_ro_source", 0))
							}
							tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+form.tag+" in a read-only transaction")
							if missing {
								want = fixture.ControlsReferenceDiagnostic("42P01", `relation "qc_ro_source" does not exist`)
								if form.tag == "CREATE TABLE AS" {
									want.Position = "37"
								} else {
									want.Position = "26"
								}
							}
							x := fixture.Executor(tx)
							if held {
								bound := tx.StmtContext(p.Ctx, stmt)
								defer bound.Close()
								x = fixture.HeldStatement{Executor: tx, Statement: bound, SQL: form.query}
							}
							fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ErrorDiagnostic(form.query, want))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.QueryTableCreateAbsent(t, run, "qc_ro")
							run(fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
						})
					}
				})
			}
		}
	}
}
