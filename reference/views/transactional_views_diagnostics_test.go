package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsDiagnostics(t *testing.T) {
	cases := []struct{ q, tag string }{
		{"CREATE VIEW txv_new AS SELECT id FROM txv_absent", "CREATE VIEW"},
		{"CREATE OR REPLACE VIEW txv_new AS SELECT id FROM txv_absent", "CREATE VIEW"},
		{"ALTER VIEW txv_absent RENAME TO txv_new", "ALTER VIEW"},
		{"DROP VIEW txv_absent", "DROP VIEW"},
		{"CREATE MATERIALIZED VIEW txv_new AS SELECT id FROM txv_source", "CREATE MATERIALIZED VIEW"},
		{"ALTER MATERIALIZED VIEW txv_absent RENAME TO txv_new", "ALTER MATERIALIZED VIEW"},
		{"DROP MATERIALIZED VIEW txv_absent", "DROP MATERIALIZED VIEW"},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
				base(fixture.Exec("INSERT INTO txv_source VALUES(7)", 1))
				var x fixture.Executor = c
				if held {
					stmt, err := c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal("ordinary Describe must not bind target/body", err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
				}
				base(fixture.Exec("SET default_transaction_read_only=on", 0))
				defer c.ExecContext(p.Ctx, "SET default_transaction_read_only=off")
				var e fixture.Executor
				var finish func(bool)
				if mode == "go" {
					tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					e = tx
					finish = func(bool) {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					e, finish = transactionalViewsBegin(t, p, b, c, mode)
				}
				want := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
				want.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, x, b.Recorder, want)
				if mode != "autocommit" {
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					finish(false)
				} else {
					finish(false)
				}
				base(fixture.Exec("SET default_transaction_read_only=off", 0))
				base(fixture.Select("SELECT id FROM txv_source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				fixture.QueryTableCreateAbsent(t, base, "txv_new")
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-direct", true: "source-prepare"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, run := fixture.QueryTableCreateSession(t, p, b)
					run(fixture.Exec("SET default_transaction_read_only=on", 0))
					defer c.ExecContext(p.Ctx, "SET default_transaction_read_only=off")
					q := "CREATE MATERIALIZED VIEW txv_new AS SELECT id FROM txv_absent WITH NO DATA"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "txv_absent" does not exist`, Position: "52"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					fixture.QueryTableCreateAbsent(t, run, "txv_new")
				})
			}
		})
	}
}
