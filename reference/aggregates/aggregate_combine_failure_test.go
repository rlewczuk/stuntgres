package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateCombineErrors(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE AGGREGATE ac_overflow(bigint) (SFUNC=pg_catalog.int8pl,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)", 0),
			fixture.Exec("CREATE TABLE ac_error_input(g integer,x bigint)", 0),
			fixture.Exec("INSERT INTO ac_error_input VALUES(1,1),(2,9223372036854775807),(2,1)", 3),
			fixture.Exec("CREATE TABLE ac_error_destination(g integer,v bigint)", 0),
			fixture.Exec("INSERT INTO ac_error_destination VALUES(9,9)", 1))
		for _, q := range []string{
			"SELECT g,ac_overflow(x) AS v FROM ac_error_input GROUP BY g ORDER BY v,g",
			"INSERT INTO ac_error_destination SELECT g,ac_overflow(x) FROM ac_error_input GROUP BY g ORDER BY g",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "bigint out of range"})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, q[0] == 'S', "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				createAggregateRun(t, p, b, c, fixture.Select("SELECT g,v FROM ac_error_destination", []string{"g", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(9)}}))
			}
		}
		const q = "SELECT ac_overflow(x) AS v FROM ac_error_input WHERE g=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "bigint out of range"}, 2)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(1), 1))
	})
}

func TestReferenceAggregateCombineCancellation(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ac_sleep_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+$2+CASE WHEN pg_sleep(CASE WHEN $2>1 THEN 30 ELSE 0 END) IS NULL THEN 0 ELSE 0 END$$", 0),
			fixture.Exec("CREATE AGGREGATE ac_sleep(integer) (SFUNC=ac_sleep_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)", 0),
			fixture.Exec("CREATE TABLE ac_cancel_input(x integer)", 0),
			fixture.Exec("INSERT INTO ac_cancel_input VALUES(1),(2),(3),(4)", 4),
			fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		const q = "SELECT ac_sleep(x) AS v FROM ac_cancel_input WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, 4)
			if rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatalf("pre-admission=%v/%v", rows, err)
			}
		}
		for _, prepared := range []bool{false, true} {
			aggregateCombineActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx, 4)
				} else {
					rows, err = c.QueryContext(ctx, q, 4)
				}
				if err == nil {
					for rows.Next() {
						var value any
						if err = rows.Scan(&value); err != nil {
							break
						}
					}
					if err == nil {
						err = rows.Err()
					}
					rows.Close()
				}
				return fixture.RelationOutcome{Err: err}
			})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(1), 1))
		}
	})
	t.Run("statement_timeout", func(t *testing.T) {
		aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE FUNCTION ac_timeout_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+$2+CASE WHEN pg_sleep(30) IS NULL THEN 0 ELSE 0 END$$", 0),
				fixture.Exec("CREATE AGGREGATE ac_timeout(integer) (SFUNC=ac_timeout_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)", 0),
				fixture.Exec("CREATE TABLE ac_timeout_input(x integer)", 0),
				fixture.Exec("INSERT INTO ac_timeout_input VALUES(1)", 1),
				fixture.Exec("SET max_parallel_workers_per_gather=0", 0))
			const q = "SELECT ac_timeout(x) AS v FROM ac_timeout_input"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			createAggregateRun(t, p, b, c, fixture.Exec("SET statement_timeout='100ms'", 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout", Context: "SQL function \"ac_timeout_step\" statement 1"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			// Dispose of this armed borrower; even RESET may itself time out.
		})
	})
}

func TestReferenceAggregateCombineParallelSafety(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE SEQUENCE ac_serial_seq", 0),
			fixture.Exec("CREATE FUNCTION ac_effect_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+nextval('ac_serial_seq')$$", 0),
			fixture.Exec("CREATE AGGREGATE ac_effect_default(integer) (SFUNC=ac_effect_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl)", 0),
			fixture.Exec("CREATE AGGREGATE ac_effect_restricted(integer) (SFUNC=ac_effect_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=RESTRICTED)", 0),
			fixture.Exec("CREATE TABLE ac_effect_input(x integer)", 0),
			fixture.Exec("INSERT INTO ac_effect_input VALUES(1),(2),(3),(4)", 4),
			fixture.Exec("SET max_parallel_workers_per_gather=7", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT setval('ac_serial_seq',1,FALSE) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT ac_effect_default(x) AS v FROM ac_effect_input", int64(10)))
		createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT currval('ac_serial_seq') AS v", int64(4)))
		createAggregateRun(t, p, b, c, fixture.Select("SELECT setval('ac_serial_seq',1,FALSE) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT ac_effect_restricted(x) AS v FROM ac_effect_input", int64(10)))
		createAggregateRun(t, p, b, c, aggregateCombineValue("SELECT currval('ac_serial_seq') AS v", int64(4)))
	})
}
