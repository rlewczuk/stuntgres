package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Complete diagnostics were independently observed in root's pinned tests-13.
func TestReferenceDatetimeExtractNotNullAtomicity(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dx_not_null(id integer,n numeric NOT NULL,f float8 NOT NULL)", 0))
		run(referenceExec("INSERT INTO dx_not_null VALUES(1,2000,2000)", 1))
		for _, tc := range []struct{ column, second, detail string }{
			{"n", "EXTRACT(bogus FROM NULL::timestamp),date_part('year',TIMESTAMP '2002-01-01')", "Failing row contains (3, null, 2002)."},
			{"f", "EXTRACT(year FROM TIMESTAMP '2002-01-01'),date_part('bogus',NULL::timestamp)", "Failing row contains (3, 2002, null)."},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("column=%s/prepared=%t", tc.column, prepared), func(t *testing.T) {
					q := "INSERT INTO dx_not_null VALUES(2,EXTRACT(year FROM TIMESTAMP '2001-01-01'),date_part('year',TIMESTAMP '2001-01-01')),(3," + tc.second + ")"
					d := controlsReferenceDiagnostic("23502", fmt.Sprintf("null value in column %q of relation \"dx_not_null\" violates not-null constraint", tc.column))
					d.Detail, d.Schema, d.Table, d.Column = tc.detail, "public", "dx_not_null", tc.column
					s := referenceErrorDiagnostic(q, d)
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,n,f FROM dx_not_null ORDER BY id", []string{"id", "n", "f"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "2000", float64(2000)}}))
				})
			}
		}
	})
}

func TestReferenceDatetimeExtractTransactionAbortRecovery(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dx_tx(id integer,v numeric)", 0))
		run(referenceExec("INSERT INTO dx_tx VALUES(1,2000)", 1))
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
				q := "SELECT date_part($1::text,$2::timestamp) AS value,EXTRACT(year FROM $2::timestamp) AS exact"
				var x referenceExecutor = c
				if prepared {
					// Birth before BEGIN; the exact held handle remains usable after
					// aborted-transaction rollback on the SAME physical owner.
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("BEGIN", 0))
				active := true
				defer func() {
					if active {
						if _, err := c.ExecContext(p.ctx, "ROLLBACK"); err != nil {
							t.Errorf("cleanup rollback: %v", err)
						}
					}
				}()
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dx_tx VALUES(2,EXTRACT(year FROM TIMESTAMP '2001-01-01'))", 1))
				for _, tc := range []struct {
					field      string
					diagnostic referenceDiagnostic
				}{
					{"bogus", datetimeExtractFieldDiagnostic("22023", "bogus", "timestamp without time zone")},
					{"year", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")},
				} {
					s := referenceErrorDiagnostic(q, tc.diagnostic, tc.field, "2002-01-01")
					s.selectRows, s.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, x, b.recorder, s)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("ROLLBACK", 0))
				active = false
				referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"value", "exact"}, []string{"FLOAT8", "NUMERIC"}, [][]any{{float64(2002), "2002"}}, "year", "2002-01-01"))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,v FROM dx_tx ORDER BY id", []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "2000"}}))
			})
		}
	})
}
