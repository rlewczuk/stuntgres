package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalAtomicity(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE fa_effect_log(v integer)", 0),
			fixture.Exec("CREATE FUNCTION fa_fail_final(integer) RETURNS text BEGIN ATOMIC INSERT INTO fa_effect_log VALUES($1); SELECT (12/(6-$1))::text; END", 0),
			fixture.Exec("CREATE AGGREGATE fa_fail(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_fail_final)", 0),
			fixture.Exec("CREATE TABLE fa_error_source(g integer,x integer)", 0),
			fixture.Exec("INSERT INTO fa_error_source VALUES(1,1),(1,2),(2,6)", 3),
			fixture.Exec("CREATE TABLE fa_error_destination(g integer,v text)", 0),
			fixture.Exec("INSERT INTO fa_error_destination VALUES(9,'old')", 1))
		// Sorting on the result blocks PostgreSQL cursor publication until all
		// groups finalize, including the group whose ending state is six.
		const q = "SELECT g,fa_fail(x) AS v FROM fa_error_source WHERE g<=$1 GROUP BY g ORDER BY v,g"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		const insert = "INSERT INTO fa_error_destination SELECT g,fa_fail(x) FROM fa_error_source WHERE g<=$1 GROUP BY g"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "SQL function \"fa_fail_final\" statement 2"}, 2)
			s.SelectRows, s.ErrorStage = true, "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			failure := fixture.ImplicitError(insert, fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "SQL function \"fa_fail_final\" statement 2"}, 2)
			failure.Prepared, failure.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
			createAggregateRun(t, p, b, c,
				fixture.Select("SELECT count(*) AS n FROM fa_effect_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
				fixture.Select("SELECT g,v FROM fa_error_destination", []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "old"}}))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "4"}}, 1))
			createAggregateRun(t, p, b, c,
				fixture.Select("SELECT v FROM fa_effect_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}),
				fixture.Exec("DELETE FROM fa_effect_log", 1))
		}
		// Prepare cannot execute a final body or its writes.
		createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM fa_effect_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.ExecContext(ctx, "CREATE AGGREGATE fa_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_fail_final)")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled CREATE=%v", err)
			}
			aggregateFinalNoResidue(t, p, b, c)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "4"}}, 1))
		}
	})
}

func TestReferenceAggregateFinalCancellation(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE fa_cancel_log(v integer)", 0),
			fixture.Exec("CREATE FUNCTION fa_sleep_final(integer) RETURNS text BEGIN ATOMIC INSERT INTO fa_cancel_log VALUES($1); SELECT CASE WHEN pg_sleep(CASE WHEN $1>1 THEN 30 ELSE 0 END) IS NULL THEN $1::text ELSE $1::text END; END", 0),
			fixture.Exec("CREATE AGGREGATE fa_sleep(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_sleep_final)", 0),
			fixture.Exec("CREATE TABLE fa_cancel_source(x integer)", 0),
			fixture.Exec("INSERT INTO fa_cancel_source VALUES(1),(2)", 2),
			fixture.Exec("CREATE TABLE fa_cancel_destination(v text)", 0))
		const q = "INSERT INTO fa_cancel_destination SELECT fa_sleep(x) FROM fa_cancel_source WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, prepared := range []bool{false, true} {
			fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL function \"fa_sleep_final\" statement 2", nil, func(ctx context.Context) fixture.RelationOutcome {
				var result sql.Result
				var err error
				if prepared {
					result, err = stmt.ExecContext(ctx, 2)
				} else {
					result, err = c.ExecContext(ctx, q, 2)
				}
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			for _, table := range []string{"fa_cancel_log", "fa_cancel_destination"} {
				createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1))
			createAggregateRun(t, p, b, c,
				aggregateFinalText("SELECT v FROM fa_cancel_destination", "1"),
				fixture.Select("SELECT v FROM fa_cancel_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Exec("DELETE FROM fa_cancel_destination", 1), fixture.Exec("DELETE FROM fa_cancel_log", 1))
		}
	})
}
