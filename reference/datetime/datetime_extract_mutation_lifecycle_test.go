package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Complete diagnostics were independently observed in root's pinned tests-13.
func TestReferenceDatetimeExtractNotNullAtomicity(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dx_not_null(id integer,n numeric NOT NULL,f float8 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO dx_not_null VALUES(1,2000,2000)", 1))
		for _, tc := range []struct{ column, second, detail string }{
			{"n", "EXTRACT(bogus FROM NULL::timestamp),date_part('year',TIMESTAMP '2002-01-01')", "Failing row contains (3, null, 2002)."},
			{"f", "EXTRACT(year FROM TIMESTAMP '2002-01-01'),date_part('bogus',NULL::timestamp)", "Failing row contains (3, 2002, null)."},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("column=%s/prepared=%t", tc.column, prepared), func(t *testing.T) {
					q := "INSERT INTO dx_not_null VALUES(2,EXTRACT(year FROM TIMESTAMP '2001-01-01'),date_part('year',TIMESTAMP '2001-01-01')),(3," + tc.second + ")"
					d := fixture.ControlsReferenceDiagnostic("23502", fmt.Sprintf("null value in column %q of relation \"dx_not_null\" violates not-null constraint", tc.column))
					d.Detail, d.Schema, d.Table, d.Column = tc.detail, "public", "dx_not_null", tc.column
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,n,f FROM dx_not_null ORDER BY id", []string{"id", "n", "f"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "2000", float64(2000)}}))
				})
			}
		}
	})
}

func TestReferenceDatetimeExtractTransactionAbortRecovery(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dx_tx(id integer,v numeric)", 0))
		run(fixture.Exec("INSERT INTO dx_tx VALUES(1,2000)", 1))
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
				q := "SELECT date_part($1::text,$2::timestamp) AS value,EXTRACT(year FROM $2::timestamp) AS exact"
				var x fixture.Executor = c
				if prepared {
					// Birth before BEGIN; the exact held handle remains usable after
					// aborted-transaction rollback on the SAME physical owner.
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
				active := true
				defer func() {
					if active {
						if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
							t.Errorf("cleanup rollback: %v", err)
						}
					}
				}()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dx_tx VALUES(2,EXTRACT(year FROM TIMESTAMP '2001-01-01'))", 1))
				for _, tc := range []struct {
					field      string
					diagnostic fixture.Diagnostic
				}{
					{"bogus", datetimeExtractFieldDiagnostic("22023", "bogus", "timestamp without time zone")},
					{"year", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")},
				} {
					s := fixture.ErrorDiagnostic(q, tc.diagnostic, tc.field, "2002-01-01")
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
				active = false
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"value", "exact"}, []string{"FLOAT8", "NUMERIC"}, [][]any{{float64(2002), "2002"}}, "year", "2002-01-01"))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM dx_tx ORDER BY id", []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "2000"}}))
			})
		}
	})
}
