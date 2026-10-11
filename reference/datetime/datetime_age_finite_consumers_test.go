package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeAgeFiniteCompositionControls(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE af_stored(id integer,t timestamp,span interval GENERATED ALWAYS AS (age(t,TIMESTAMP '2004-04-30')) STORED,CONSTRAINT af_finite CHECK(isfinite(t)))", 0))
		run(fixture.Exec("CREATE TABLE af_defaults(id integer,span interval DEFAULT age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30'),midnight interval DEFAULT age(CURRENT_DATE::timestamp))", 0))
		run(fixture.Exec("CREATE INDEX af_span_index ON af_stored ((age(t,TIMESTAMP '2004-04-30')))", 0))
		run(fixture.Exec("CREATE INDEX af_finite_index ON af_stored ((isfinite(t)))", 0))
		run(fixture.Exec("CREATE VIEW af_view AS SELECT id,age(t,TIMESTAMP '2004-04-30') AS span,isfinite(t) AS finite,age(CURRENT_DATE::timestamp) AS midnight FROM af_stored WHERE isfinite(t)", 0))
		run(fixture.Exec("INSERT INTO af_stored(id,t) VALUES(1,TIMESTAMP '2004-06-01')", 1))
		run(fixture.Exec("INSERT INTO af_defaults(id) VALUES(1)", 1))
		view := fixture.Select("SELECT id,span,finite,midnight FROM af_view ORDER BY id", []string{"id", "span", "finite", "midnight"}, []string{"INT4", "INTERVAL", "BOOL", "INTERVAL"}, [][]any{{int64(1), "1 mon 1 day", true, "00:00:00"}})
		// Only computed columns use computed-cursor metadata; id is a table column.
		datetimeArithmeticBoth(t, p, b, c, view)
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT span,midnight FROM af_defaults ORDER BY id", []string{"span", "midnight"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 mon 1 day", "00:00:00"}}))
		datetimeAgeFiniteBoth(t, p, b, c, fixture.Select("SELECT age(MAX(t),MIN(t)) AS span,isfinite(MAX(t)) AS finite FROM af_stored", []string{"span", "finite"}, []string{"INTERVAL", "BOOL"}, [][]any{{"00:00:00", true}}))
		datetimeAgeFiniteValue(t, p, b, c, "CASE WHEN true THEN age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30') ELSE age(TIMESTAMP 'infinity',TIMESTAMP 'infinity') END", "INTERVAL", "1 mon 1 day")
		for _, tc := range []struct{ q, message string }{
			{"CREATE TABLE af_stable_generated(t timestamp,span interval GENERATED ALWAYS AS (age(t)) STORED)", "generation expression is not immutable"},
			{"CREATE INDEX af_stable_index ON af_stored ((age(t)))", "functions in index expression must be marked IMMUTABLE"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
			})
		}
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "af_stored" violates check constraint "af_finite"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, infinity, infinity).", "public", "af_stored", "af_finite"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO af_stored(id,t) VALUES(2,TIMESTAMP '2004-07-01'),(3,TIMESTAMP 'infinity')", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(view) // No first-row publication from the failed multirow statement.
		}
		// Fast pq requests race cancellation; native precancellation is an
		// ordinary context-error contract (docs/TESTING.md), not a PG oracle.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{"SELECT age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30')", "INSERT INTO af_stored(id,t) VALUES(2,TIMESTAMP '2004-07-01')"} {
				if _, err := c.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled %s: %v", q, err)
				}
			}
		}
		run(view)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(view)
			run(fixture.Exec("INSERT INTO af_stored(id,t) VALUES(2,TIMESTAMP '2004-07-01')", 1))
			run(fixture.Select("SELECT span,isfinite(t) AS finite FROM af_stored WHERE id=2", []string{"span", "finite"}, []string{"INTERVAL", "BOOL"}, [][]any{{"2 mons 1 day", true}}))
			run(fixture.Exec("INSERT INTO af_defaults(id) VALUES(2)", 1))
			run(fixture.Select("SELECT span,midnight FROM af_defaults WHERE id=2", []string{"span", "midnight"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 mon 1 day", "00:00:00"}}))
			// Restored CHECK is evaluated, not merely printed in a dump.
			s := fixture.ErrorDiagnostic("INSERT INTO af_stored(id,t) VALUES(3,TIMESTAMP 'infinity')", d)
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT id FROM af_stored ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		}
	})
}
