package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReferenceDatetimeZoneActiveCancellation(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dz_active_input(id INTEGER,z TIMESTAMPTZ,zone TEXT)", 0))
		run(referenceExec("CREATE TABLE dz_active_out(id INTEGER,z TIMESTAMPTZ)", 0))
		run(referenceExec("INSERT INTO dz_active_input VALUES(1,'2018-03-10 17:00+00','America/New_York')", 1))
		run(referenceExec("INSERT INTO dz_active_out VALUES(0,'2004-01-01+00')", 1))
		// Finite 128^3 source occurrences, with a row-dependent selected calendar
		// provider. Main calibrates this draft workload against the actual pinned
		// oracle; completion before admission is failure, not cancellation proof.
		query := "INSERT INTO dz_active_out(id,z) SELECT a.id,date_add(a.z,INTERVAL '1 day',b.zone) FROM dz_active_input a CROSS JOIN dz_active_input b CROSS JOIN dz_active_input c"
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
				// Subcases remain isolated even if admission/cancellation assertions
				// fail before the successful-path fixture reduction.
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, q := range []string{"DELETE FROM dz_active_input WHERE id>1", "DELETE FROM dz_active_out WHERE id>0"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Errorf("cancellation fixture cleanup %s: %v", q, err)
						}
					}
				})
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				values := make([]string, 0, 127)
				for id := 2; id <= 128; id++ {
					values = append(values, fmt.Sprintf("(%d,'2018-03-10 17:00+00','America/New_York')", id))
				}
				run(referenceExec("INSERT INTO dz_active_input VALUES"+strings.Join(values, ","), 127))
				run(referenceSelect("SELECT COUNT(*) AS n FROM dz_active_input", []string{"n"}, []string{"INT8"}, [][]any{{int64(128)}}))
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				// Existing public-reference helper gates PG on this exact query/PID
				// and native on actual provider+worker stack, with deadline and join.
				binaryConversionActiveCancel(t, p, b, c, query, "sqltype.addTimestampInterval", func(ctx context.Context) relationOutcome {
					var result sql.Result
					var err error
					if stmt != nil {
						result, err = stmt.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, query)
					}
					return relationOutcome{result: result, err: err}
				})
				run(referenceSelect("SELECT id,z::TEXT AS z FROM dz_active_out ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "2004-01-01 00:00:00+00"}}))
				run(referenceExec("DELETE FROM dz_active_input WHERE id>1", 127))
				if stmt == nil {
					run(referenceExec(query, 1))
				} else {
					referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}, b.recorder, referenceExec(query, 1))
				}
				run(referenceSelect("SELECT id,z::TEXT AS z FROM dz_active_out ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "2004-01-01 00:00:00+00"}, {int64(1), "2018-03-11 16:00:00+00"}}))
				run(referenceExec("DELETE FROM dz_active_out WHERE id=1", 1))
				tx, err := c.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT timezone('UTC',z)::TEXT AS z FROM dz_active_input", []string{"z"}, []string{"TEXT"}, [][]any{{"2018-03-10 17:00:00"}}))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}
