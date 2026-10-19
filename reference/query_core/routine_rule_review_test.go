package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineRuleQualificationPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_rule_review")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA routine_rule_review",
			"CREATE FUNCTION routine_rule_review.positive(integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1 > 0$$",
			"CREATE TABLE routine_rule_review.input(x integer)",
			"CREATE TABLE routine_rule_review.positive_log(x integer)",
			"CREATE TABLE routine_rule_review.nonpositive_log(x integer)",
			"CREATE RULE capture_positive AS ON INSERT TO routine_rule_review.input WHERE routine_rule_review.positive(NEW.x) DO ALSO INSERT INTO routine_rule_review.positive_log VALUES (NEW.x)",
			"CREATE RULE capture_nonpositive AS ON INSERT TO routine_rule_review.input WHERE NOT (routine_rule_review.positive(NEW.x)) DO ALSO INSERT INTO routine_rule_review.nonpositive_log VALUES (NEW.x)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_rule_review.input VALUES (-2), (0), (3)", 3))
		assertData := func(conn *sql.Conn, recorder *fixture.Recorder, input, positive, nonpositive [][]any) {
			t.Helper()
			for _, check := range []struct {
				table string
				rows  [][]any
			}{
				{"input", input},
				{"positive_log", positive},
				{"nonpositive_log", nonpositive},
			} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Select("SELECT x FROM routine_rule_review."+check.table+" ORDER BY x", []string{"x"}, []string{"INT4"}, check.rows))
			}
		}
		assertData(c, b.Recorder, [][]any{{int64(-2)}, {int64(0)}, {int64(3)}}, [][]any{{int64(3)}}, [][]any{{int64(-2)}, {int64(0)}})
		if b.PG {
			return
		}

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
		assertData(c, b.Recorder, [][]any{{int64(-2)}, {int64(0)}, {int64(3)}}, [][]any{{int64(3)}}, [][]any{{int64(-2)}, {int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_rule_review.input VALUES (-4), (5)", 2))
		assertData(c, b.Recorder, [][]any{{int64(-4)}, {int64(-2)}, {int64(0)}, {int64(3)}, {int64(5)}}, [][]any{{int64(3)}, {int64(5)}}, [][]any{{int64(-4)}, {int64(-2)}, {int64(0)}})

		// Replay exactly the emitted stream, not a separately authored schema.
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS routine_rule_review CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted-stream PostgreSQL replay", err)
			}
			assertData(pg, p.PostgresRecord, [][]any{{int64(-2)}, {int64(0)}, {int64(3)}}, [][]any{{int64(3)}}, [][]any{{int64(-2)}, {int64(0)}})
			fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Exec("INSERT INTO routine_rule_review.input VALUES (-6), (7)", 2))
			assertData(pg, p.PostgresRecord, [][]any{{int64(-6)}, {int64(-2)}, {int64(0)}, {int64(3)}, {int64(7)}}, [][]any{{int64(3)}, {int64(7)}}, [][]any{{int64(-6)}, {int64(-2)}, {int64(0)}})
			if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies = true; DROP SCHEMA routine_rule_review CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
