package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These scenarios distinguish detached runtime evaluation from definition cooking.
// Every provider has a real operand; none depends on elapsed workload or a host hook.
func TestReferenceExecutionPauseRuntimeOwners(t *testing.T) {
	for _, tc := range []struct {
		name, query, point string
		setup              []string
		args               []any
		before, after      fixture.Step
		column, typ        string
		value              any
	}{
		{
			name: "index_build", query: "CREATE UNIQUE INDEX execution_pause_index ON execution_pause_runtime(id) WHERE value LIKE 'a%'", point: "text.like.match",
			setup:  []string{"CREATE TABLE execution_pause_runtime(id integer,value text)", "INSERT INTO execution_pause_runtime VALUES(1,'alpha'),(2,'alpine')"},
			before: fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='execution_pause_index' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			after:  fixture.Select("SELECT indisvalid,indisready FROM pg_index WHERE indexrelid='execution_pause_index'::regclass", []string{"indisvalid", "indisready"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
		},
		{
			name: "reindex", query: "REINDEX INDEX execution_pause_index", point: "text.like.match",
			setup:  []string{"CREATE TABLE execution_pause_runtime(id integer,value text)", "INSERT INTO execution_pause_runtime VALUES(1,'alpha'),(2,'alpine')", "CREATE UNIQUE INDEX execution_pause_index ON execution_pause_runtime(id) WHERE value LIKE 'a%'"},
			before: fixture.Select("SELECT indisvalid,indisready FROM pg_index WHERE indexrelid='execution_pause_index'::regclass", []string{"indisvalid", "indisready"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
			after:  fixture.Select("SELECT id,value FROM execution_pause_runtime ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "alpine"}}),
		},
		{
			name: "alter_default", query: "ALTER TABLE execution_pause_runtime ADD COLUMN n integer DEFAULT json_array_length('[1,2]')", point: "json.array_length",
			setup:  []string{"CREATE TABLE execution_pause_runtime(id integer)", "INSERT INTO execution_pause_runtime VALUES(1),(2)"},
			before: fixture.Select("SELECT count(*) AS n FROM information_schema.columns WHERE table_schema='public' AND table_name='execution_pause_runtime' AND column_name='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			after:  fixture.Select("SELECT id,n FROM execution_pause_runtime ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}}),
		},
		{
			name: "alter_check", query: "ALTER TABLE execution_pause_runtime ADD CONSTRAINT execution_pause_check CHECK(value LIKE 'a%')", point: "text.like.match",
			setup:  []string{"CREATE TABLE execution_pause_runtime(value text)", "INSERT INTO execution_pause_runtime VALUES('alpha'),('alpine')"},
			before: fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conname='execution_pause_check'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			after:  fixture.Select("SELECT convalidated FROM pg_constraint WHERE conname='execution_pause_check'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}),
		},
		{
			name: "alter_type", query: "ALTER TABLE execution_pause_runtime ALTER COLUMN value TYPE integer USING json_array_length(value::json)", point: "json.array_length",
			setup:  []string{"CREATE TABLE execution_pause_runtime(id integer,value text)", "INSERT INTO execution_pause_runtime VALUES(1,'[1]'),(2,'[1,2]')"},
			before: fixture.Select("SELECT id,value FROM execution_pause_runtime ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "[1]"}, {int64(2), "[1,2]"}}),
			after:  fixture.Select("SELECT id,value FROM execution_pause_runtime ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
		},
		{
			name: "domain_validate", query: "ALTER DOMAIN execution_pause_domain VALIDATE CONSTRAINT execution_pause_check", point: "text.like.match",
			setup:  []string{"CREATE DOMAIN execution_pause_domain AS text", "CREATE TABLE execution_pause_runtime(value execution_pause_domain)", "INSERT INTO execution_pause_runtime VALUES('alpha'),('alpine')", "ALTER DOMAIN execution_pause_domain ADD CONSTRAINT execution_pause_check CHECK(VALUE LIKE 'a%') NOT VALID"},
			before: fixture.Select("SELECT convalidated FROM pg_constraint WHERE conname='execution_pause_check'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}),
			after:  fixture.Select("SELECT convalidated FROM pg_constraint WHERE conname='execution_pause_check'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}),
		},
		{
			name: "domain_input", query: "INSERT INTO execution_pause_runtime VALUES($1)", point: "text.like.match", args: []any{"alpha"},
			setup:  []string{"CREATE DOMAIN execution_pause_domain AS text CHECK(VALUE LIKE 'a%')", "CREATE TABLE execution_pause_runtime(value execution_pause_domain)"},
			before: fixture.Select("SELECT count(*) AS n FROM execution_pause_runtime", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			after:  fixture.Select("SELECT value::text AS value FROM execution_pause_runtime", []string{"value"}, []string{"TEXT"}, [][]any{{"alpha"}}),
		},
		{
			name: "statistics", query: "ANALYZE execution_pause_runtime", point: "json.array_length",
			setup:  []string{"CREATE TABLE execution_pause_runtime(value json)", "INSERT INTO execution_pause_runtime VALUES('[1]'),('[1,2]')", "CREATE STATISTICS execution_pause_statistics ON (json_array_length(value)) FROM execution_pause_runtime"},
			before: fixture.Select("SELECT count(*) AS n FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='execution_pause_statistics'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			after:  fixture.Select("SELECT count(*) AS n FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='execution_pause_statistics'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
		},
		{
			name: "sql_execute_actual", query: "EXECUTE execution_pause_sql(json_array_length('[1,2]'))", point: "json.array_length",
			setup: []string{"PREPARE execution_pause_sql(integer) AS SELECT $1 AS value"}, column: "value", typ: "INT4", value: int64(2),
		},
		{
			name: "pl_domain_assignment", query: "SELECT execution_pause_pl($1) AS value", point: "text.like.match", args: []any{"alpha"},
			setup: []string{"CREATE DOMAIN execution_pause_domain AS text CHECK(VALUE LIKE 'a%')", "CREATE FUNCTION execution_pause_pl(x text) RETURNS text LANGUAGE plpgsql AS $$ DECLARE result execution_pause_domain; BEGIN result := x; RETURN result::text; END $$"}, column: "value", typ: "TEXT", value: "alpha",
		},
		{
			name: "pl_field_assignment", query: "SELECT execution_pause_pl($1) AS value", point: "text.like.match", args: []any{"alpha"},
			setup: []string{"CREATE DOMAIN execution_pause_domain AS text CHECK(VALUE LIKE 'a%')", "CREATE TYPE execution_pause_composite AS (value execution_pause_domain)", "CREATE FUNCTION execution_pause_pl(x text) RETURNS text LANGUAGE plpgsql AS $$ DECLARE result execution_pause_composite; BEGIN result.value := x; RETURN result.value::text; END $$"}, column: "value", typ: "TEXT", value: "alpha",
		},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range tc.setup {
						count := int64(0)
						if len(q) >= 6 && q[:6] == "INSERT" {
							count = 2
						}
						run(fixture.Exec(q, count))
					}
					defer func() {
						ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						_, _ = c.ExecContext(ctx, "ROLLBACK")
						if tc.name == "sql_execute_actual" {
							_, _ = c.ExecContext(ctx, "DEALLOCATE execution_pause_sql")
						}
					}()
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = c.PrepareContext(p.Ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					execute := func(ctx context.Context) fixture.RelationOutcome {
						if tc.column != "" {
							if held != nil {
								r, e := held.QueryContext(ctx, tc.args...)
								return fixture.RelationOutcome{Rows: r, Err: e}
							}
							r, e := c.QueryContext(ctx, tc.query, tc.args...)
							return fixture.RelationOutcome{Rows: r, Err: e}
						}
						if held != nil {
							r, e := held.ExecContext(ctx, tc.args...)
							return fixture.RelationOutcome{Result: r, Err: e}
						}
						r, e := c.ExecContext(ctx, tc.query, tc.args...)
						return fixture.RelationOutcome{Result: r, Err: e}
					}
					if tc.before.Query != "" {
						run(tc.before)
					}
					if !b.PG {
						if tc.name != "reindex" {
							run(fixture.Exec("BEGIN", 0))
						}
						out, cleanup := executionPauseRuntimeDemand(t, p, tc.point, true, execute)
						cleanup()
						fixture.LexicalOrdinary(t, out.Err, context.Canceled)
						if out.Rows != nil || out.Result != nil {
							t.Fatal("canceled runtime published output", out)
						}
						if tc.name != "reindex" {
							run(fixture.Exec("ROLLBACK", 0))
						}
						if tc.before.Query != "" {
							run(tc.before)
						}
					}
					// Transactional commands roll back one successful execution,
					// then reuse the same definition and commit. REINDEX owns an
					// autocommit maintenance lifecycle, not an explicit transaction.
					for attempt := range 2 {
						if tc.name != "reindex" {
							run(fixture.Exec("BEGIN", 0))
						}
						point := tc.point
						if b.PG {
							point = ""
						}
						out, cleanup := executionPauseRuntimeDemand(t, p, point, false, execute)
						defer cleanup()
						if out.Err != nil {
							t.Fatal("resumed runtime", out.Err)
						}
						if tc.column != "" {
							if out.Rows == nil || out.Result != nil {
								t.Fatal("runtime query output shape", out)
							}
							rows := out.Rows
							defer rows.Close()
							columns, err := rows.Columns()
							if err != nil || len(columns) != 1 || columns[0] != tc.column {
								t.Fatal(columns, err)
							}
							types, err := rows.ColumnTypes()
							if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != tc.typ {
								t.Fatal(types, err)
							}
							if !rows.Next() {
								t.Fatal("missing runtime row", rows.Err())
							}
							var value any
							if err := rows.Scan(&value); err != nil {
								t.Fatal(err)
							}
							if bytes, ok := value.([]byte); ok {
								value = string(bytes)
							}
							if value != tc.value || rows.Next() || rows.Err() != nil {
								t.Fatal("runtime row", value, rows.Err())
							}
							rows.Close()
						} else {
							want := int64(0)
							if tc.name == "domain_input" {
								want = 1
							}
							if out.Result == nil || out.Rows != nil {
								t.Fatal("runtime command output shape", out)
							}
							count, err := out.Result.RowsAffected()
							if err != nil || count != want {
								t.Fatal("runtime affected", count, err)
							}
						}
						if tc.after.Query != "" {
							run(tc.after)
						}
						if tc.name == "reindex" {
							run(tc.before)
						}
						cleanup()
						if tc.name != "reindex" {
							if attempt == 0 {
								run(fixture.Exec("ROLLBACK", 0))
								if tc.before.Query != "" {
									run(tc.before)
								}
							} else {
								run(fixture.Exec("COMMIT", 0))
							}
						}
					}
					run(fixture.Select("SELECT 11 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(11)}}))
				})
			})
		}
	}
}

func executionPauseRuntimeDemand(t *testing.T, p *fixture.Pair, point string, canceled bool, execute func(context.Context) fixture.RelationOutcome) (fixture.RelationOutcome, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	if point == "" {
		return execute(ctx), cancel
	}
	pause := stuntgres.NewExecutionPause(point)
	ctx = stuntgres.WithExecutionPause(ctx, pause)
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	cleanup := func() { cancel(); pause.Resume() }
	defer func() {
		if !joined {
			cleanup()
			out := <-done
			if out.Rows != nil {
				out.Rows.Close()
			}
		}
	}()
	go func() { done <- execute(ctx) }()
	admitted := make(chan error, 1)
	observe, stop := context.WithCancel(ctx)
	go func() { admitted <- pause.Wait(observe) }()
	select {
	case err := <-admitted:
		stop()
		if err != nil {
			t.Fatal("runtime provider admission", point, err)
		}
	case out := <-done:
		joined = true
		stop()
		<-admitted
		if out.Rows != nil {
			out.Rows.Close()
		}
		t.Fatal("runtime completed before provider admission", point, out.Err)
	}
	var healthy int
	if err := p.Native.QueryRowContext(ctx, "SELECT 7").Scan(&healthy); err != nil || healthy != 7 {
		t.Fatal("runtime pause blocked independent owner", healthy, err)
	}
	if canceled {
		cancel()
	} else {
		pause.Resume()
	}
	out := <-done
	joined = true
	if canceled && !errors.Is(out.Err, context.Canceled) {
		t.Fatal("runtime cancellation classification", out.Err)
	}
	return out, cleanup
}

func TestReferenceExecutionPauseConcurrentIndex(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE execution_pause_runtime(id integer,value text)", 0))
				run(fixture.Exec("INSERT INTO execution_pause_runtime VALUES(1,'alpha'),(2,'alpine')", 2))
				// Partial concurrent indexes are not an existing capability.
				// Expression keys are: the detached build must really evaluate
				// this row-dependent LIKE, not a fabricated direct-key provider.
				const query = "CREATE INDEX CONCURRENTLY execution_pause_index ON execution_pause_runtime ((value LIKE 'a%'))"
				var held *sql.Stmt
				if prepared {
					var err error
					held, err = c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				execute := func(ctx context.Context) fixture.RelationOutcome {
					if held != nil {
						r, err := held.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					}
					r, err := c.ExecContext(ctx, query)
					return fixture.RelationOutcome{Result: r, Err: err}
				}
				if !b.PG {
					out, cleanup := executionPauseRuntimeDemand(t, p, "text.like.match", true, execute)
					cleanup()
					if out.Err != context.Canceled || out.Rows != nil || out.Result != nil {
						t.Fatal("concurrent build cancellation output", out)
					}
					// The actual first build poll is after descriptor publication,
					// before its ready-root publication. Cancellation retains that
					// invalid shell; it must not pretend to be a valid usable index.
					run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='execution_pause_index'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
					run(fixture.Exec("DROP INDEX execution_pause_index", 0))
				}
				for attempt := range 2 {
					point := "text.like.match"
					if b.PG {
						point = ""
					}
					out, cleanup := executionPauseRuntimeDemand(t, p, point, false, execute)
					cleanup()
					if out.Err != nil || out.Result == nil || out.Rows != nil {
						t.Fatal("concurrent build Resume", out)
					}
					count, err := out.Result.RowsAffected()
					if err != nil || count != 0 {
						t.Fatal(count, err)
					}
					run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='execution_pause_index'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
					run(fixture.Select("SELECT id,value FROM execution_pause_runtime ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "alpine"}}))
					if attempt == 0 {
						run(fixture.Exec("DROP INDEX execution_pause_index", 0))
					}
				}
				run(fixture.Select("SELECT 11 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(11)}}))
			})
		})
	}
}
