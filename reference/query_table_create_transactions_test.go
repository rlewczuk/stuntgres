package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryTableCreateTransactions(t *testing.T) {
	for _, form := range []struct{ query, tag string }{
		{"CREATE TABLE qc_tx AS SELECT 7 AS i", "CREATE TABLE AS"},
		{"SELECT 7 AS i INTO qc_tx", "SELECT INTO"},
	} {
		for _, route := range []string{"direct", "prepare", "held"} {
			t.Run(form.tag+"/"+route, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						conn, run := queryTableCreateSession(t, p, b)
						var held *sql.Stmt
						if route == "held" {
							var err error
							held, err = conn.PrepareContext(p.ctx, form.query)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
						}
						tx, err := conn.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						executor := referenceExecutor(tx)
						if held != nil {
							stmt := tx.StmtContext(p.ctx, held)
							defer stmt.Close()
							executor = referenceHeldStatement{referenceExecutor: tx, statement: stmt, query: form.query}
						}
						s := referenceExec(form.query, 1)
						if !b.pg {
							s = referenceErrorDiagnostic(form.query, controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
							s.errorStage = "execute"
							if route == "prepare" {
								s.errorStage = "prepare"
							}
						}
						s.prepared = route == "prepare"
						referenceRunStep(t, p.ctx, executor, b.recorder, s)
						if b.pg {
							referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT i FROM qc_tx", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						} else {
							referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						queryTableCreateAbsent(t, run, b.pg, "qc_tx")
						run(referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
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
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						t.Run(b.name, func(t *testing.T) {
							conn, run := queryTableCreateSession(t, p, b)
							run(referenceExec("CREATE TABLE qc_ro_source(i INT)", 0))
							var stmt *sql.Stmt
							if held {
								var err error
								stmt, err = conn.PrepareContext(p.ctx, form.query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
							}
							if missing {
								run(referenceExec("DROP TABLE qc_ro_source", 0))
							}
							tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							want := controlsReferenceDiagnostic("25006", "cannot execute "+form.tag+" in a read-only transaction")
							if b.pg && missing {
								want = controlsReferenceDiagnostic("42P01", `relation "qc_ro_source" does not exist`)
								if form.tag == "CREATE TABLE AS" {
									want.Position = "37"
								} else {
									want.Position = "26"
								}
							}
							if held && !b.pg {
								// Conn-bound Stmt -> Tx.StmtContext reprepares inside the block.
								// Its sticky native Prepare exclusion surfaces from held Exec;
								// this route never reaches the earlier Execute read-only gate.
								want = controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
							}
							x := referenceExecutor(tx)
							if held {
								bound := tx.StmtContext(p.ctx, stmt)
								defer bound.Close()
								x = referenceHeldStatement{referenceExecutor: tx, statement: bound, query: form.query}
							}
							referenceRunStep(t, p.ctx, x, b.recorder, referenceErrorDiagnostic(form.query, want))
							referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							queryTableCreateAbsent(t, run, b.pg, "qc_ro")
							run(referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
						})
					}
				})
			}
		}
	}
}
