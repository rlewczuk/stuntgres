package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExecutionPauseDelete(t *testing.T) {
	for _, tc := range []struct {
		name, sql, point, second string
		class                    stuntgres.WorkClass
		args                     []any
		setup                    []string
	}{
		{name: "predicate", sql: "DELETE FROM execution_pause_delete WHERE value LIKE $1", point: "text.like.match", class: stuntgres.WorkPatternMatch, args: []any{"a%"}},
		{name: "using", sql: `DELETE FROM execution_pause_delete AS d USING jsonb_each('{"1":0,"3":0}'::jsonb) AS s WHERE d.id=s.key::integer AND d.value LIKE $1 RETURNING d.id,d.value`, point: "json.each.next", class: stuntgres.WorkJSONBObjectExpand, second: "value", args: []any{"a%"}},
		{name: "returning", sql: "DELETE FROM execution_pause_delete WHERE id IN (1,3) RETURNING id,value LIKE $1 AS matched", point: "text.like.match", class: stuntgres.WorkPatternMatch, second: "matched", args: []any{"a%"}},
		{name: "unique_index", sql: "DELETE FROM execution_pause_delete WHERE id IN (1,3)", point: "text.like.match", class: stuntgres.WorkPatternMatch, setup: []string{"CREATE UNIQUE INDEX execution_pause_partial ON execution_pause_delete(id) WHERE value LIKE 'a%'"}},
		{name: "before_when", sql: "DELETE FROM execution_pause_delete WHERE id IN (1,3)", point: "text.like.match", class: stuntgres.WorkPatternMatch, setup: []string{"CREATE FUNCTION execution_pause_delete_trigger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN OLD; END $$", "CREATE TRIGGER execution_pause_delete_trigger BEFORE DELETE ON execution_pause_delete FOR EACH ROW WHEN (OLD.value LIKE 'a%') EXECUTE FUNCTION execution_pause_delete_trigger()"}},
		{name: "after_when", sql: "DELETE FROM execution_pause_delete WHERE id IN (1,3)", point: "text.like.match", class: stuntgres.WorkPatternMatch, setup: []string{"CREATE FUNCTION execution_pause_delete_trigger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(64018328); RETURN OLD; END $$", "CREATE TRIGGER execution_pause_delete_trigger AFTER DELETE ON execution_pause_delete FOR EACH ROW WHEN (OLD.value LIKE 'a%') EXECUTE FUNCTION execution_pause_delete_trigger()"}},
		{name: "referential_default", sql: "DELETE FROM execution_pause_delete WHERE id IN (1,3)", point: "json.array_length", class: stuntgres.WorkJSONArrayCount, setup: []string{"CREATE TABLE execution_pause_child(pid integer DEFAULT json_array_length('[0,0]') REFERENCES execution_pause_delete(id) ON DELETE SET DEFAULT)", "CREATE FUNCTION execution_pause_child_trigger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(64018328); RETURN NEW; END $$", "CREATE TRIGGER execution_pause_child_trigger BEFORE UPDATE ON execution_pause_child FOR EACH ROW EXECUTE FUNCTION execution_pause_child_trigger()"}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
					defer func() {
						cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
							t.Error("DELETE transaction cleanup", err)
						}
					}()
					run(fixture.Exec("CREATE TABLE execution_pause_delete(id integer PRIMARY KEY,value text)", 0))
					seed := func() {
						run(fixture.Exec("INSERT INTO execution_pause_delete VALUES(1,'alpha'),(2,'beta'),(3,'alpine')", 3))
					}
					seed()
					for _, statement := range tc.setup {
						run(fixture.Exec(statement, 0))
					}
					if tc.name == "referential_default" {
						run(fixture.Exec("INSERT INTO execution_pause_child VALUES(1),(3)", 2))
					}
					var normal *sql.Stmt
					if prepared {
						var err error
						normal, err = c.PrepareContext(p.Ctx, tc.sql)
						if err != nil {
							t.Fatal(err)
						}
						defer normal.Close()
					}
					execute := func(ctx context.Context) fixture.RelationOutcome {
						if tc.second == "" {
							if normal != nil {
								r, err := normal.ExecContext(ctx, tc.args...)
								return fixture.RelationOutcome{Result: r, Err: err}
							}
							r, err := c.ExecContext(ctx, tc.sql, tc.args...)
							return fixture.RelationOutcome{Result: r, Err: err}
						}
						if normal != nil {
							rows, err := normal.QueryContext(ctx, tc.args...)
							return fixture.RelationOutcome{Rows: rows, Err: err}
						}
						rows, err := c.QueryContext(ctx, tc.sql, tc.args...)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					remaining := func() {
						run(fixture.Select("SELECT id,value FROM execution_pause_delete ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "beta"}}))
						if tc.name == "referential_default" {
							run(fixture.Select("SELECT pid FROM execution_pause_child ORDER BY pid", []string{"pid"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
						}
					}
					executionPauseDeleteDemand(t, p, b, tc.point, execute, tc.second)
					remaining()
					if tc.name == "unique_index" {
						run(fixture.Exec("DROP INDEX execution_pause_partial", 0))
					}
					truncate := "TRUNCATE execution_pause_delete"
					if tc.name == "referential_default" {
						truncate = "TRUNCATE execution_pause_child,execution_pause_delete"
					}
					run(fixture.Exec(truncate, 0))
					seed()
					if tc.name == "referential_default" {
						run(fixture.Exec("INSERT INTO execution_pause_child VALUES(1),(3)", 2))
					}
					// Recreate AFTER the committed rows. The matching private
					// TestExecutionPauseDeleteIndexCache checks the actual store's
					// absent committed-row outputs, not just the fresh index name.
					if tc.name == "unique_index" {
						run(fixture.Exec(tc.setup[0], 0))
					}

					// A fixed independent lock keeps PostgreSQL's RETURNING (or
					// AFTER trigger body) admitted. Native cancellation instead
					// waits at the genuine typed work-admission class.
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					const key = int64(64018328)
					if _, err := holder.ExecContext(p.Ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
						t.Fatal(err)
					}
					locked := true
					unlock := func() {
						cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						var unlocked bool
						if err := holder.QueryRowContext(cleanup, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil || !unlocked {
							t.Error("DELETE admission lock cleanup", unlocked, err)
						}
						locked = false
					}
					defer func() {
						if locked {
							unlock()
						}
					}()
					cancelSQL := tc.sql + ",pg_advisory_xact_lock(64018328) AS held"
					if tc.second == "" {
						cancelSQL = tc.sql + " RETURNING pg_advisory_xact_lock(64018328) AS held"
					}
					if tc.name == "after_when" || tc.name == "referential_default" {
						cancelSQL = tc.sql
					}
					var held *sql.Stmt
					if prepared {
						held, err = c.PrepareContext(p.Ctx, cancelSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					postgresContext := ""
					switch tc.name {
					case "after_when":
						postgresContext = "SQL statement \"SELECT pg_advisory_xact_lock(64018328)\"\nPL/pgSQL function execution_pause_delete_trigger() line 1 at PERFORM"
					case "referential_default":
						postgresContext = "SQL statement \"SELECT pg_advisory_xact_lock(64018328)\"\nPL/pgSQL function execution_pause_child_trigger() line 1 at PERFORM\nSQL statement \"UPDATE ONLY \"public\".\"execution_pause_child\" SET \"pid\" = DEFAULT WHERE $1 OPERATOR(pg_catalog.=) \"pid\"\""
					}
					run(fixture.Exec("BEGIN", 0))
					fixture.BinaryConversionActiveCancelWithContext(t, p, b, c, cancelSQL, tc.class, postgresContext, func(ctx context.Context) fixture.RelationOutcome {
						if held != nil {
							r, err := held.ExecContext(ctx, tc.args...)
							return fixture.RelationOutcome{Result: r, Err: err}
						}
						r, err := c.ExecContext(ctx, cancelSQL, tc.args...)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if held != nil {
						held.Close()
					}
					run(fixture.Exec("ROLLBACK", 0))
					unlock()
					run(fixture.Select("SELECT id,value FROM execution_pause_delete ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "beta"}, {int64(3), "alpine"}}))
					if tc.name == "referential_default" {
						run(fixture.Select("SELECT pid FROM execution_pause_child ORDER BY pid", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
					}
					// Repeat the same held/direct definition on the recovered
					// physical owner. No replacement statement or weakened query.
					executionPauseDeleteDemand(t, p, b, tc.point, execute, tc.second)
					remaining()
					run(fixture.Select("SELECT 11 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(11)}}))
				})
			})
		}
	}
}

func executionPauseDeleteDemand(t *testing.T, p *fixture.Pair, b fixture.Backend, point string, execute func(context.Context) fixture.RelationOutcome, second string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	var pause *stuntgres.ExecutionPause
	if !b.PG {
		pause = stuntgres.NewExecutionPause(point)
		defer pause.Resume()
		ctx = stuntgres.WithExecutionPause(ctx, pause)
	}
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		if pause != nil {
			pause.Resume()
		}
		if !joined {
			out := <-done
			if out.Rows != nil {
				out.Rows.Close()
			}
		}
	}()
	go func() { done <- execute(ctx) }()
	if pause != nil {
		admitted := make(chan error, 1)
		observe, stop := context.WithCancel(ctx)
		go func() { admitted <- pause.Wait(observe) }()
		select {
		case err := <-admitted:
			stop()
			if err != nil {
				t.Fatal("DELETE provider admission", point, err)
			}
		case out := <-done:
			joined = true
			stop()
			<-admitted
			if out.Rows != nil {
				out.Rows.Close()
			}
			t.Fatal("DELETE completed before provider admission", point, out.Err)
		}
		var count int
		if err := p.Native.QueryRowContext(ctx, "SELECT count(*) FROM execution_pause_delete").Scan(&count); err != nil || count != 3 {
			t.Fatal("paused DELETE blocked independent owner or published deletion", count, err)
		}
		pause.Resume()
	}
	out := <-done
	joined = true
	if out.Err != nil {
		if out.Rows != nil {
			out.Rows.Close()
		}
		t.Fatal("DELETE normal Resume", out.Err)
	}
	if second == "" {
		if out.Result == nil || out.Rows != nil {
			t.Fatal("DELETE result shape")
		}
		count, err := out.Result.RowsAffected()
		if err != nil || count != 2 {
			t.Fatal("DELETE affected count", count, err)
		}
		return
	}
	if out.Rows == nil || out.Result != nil {
		t.Fatal("DELETE RETURNING result shape")
	}
	rows := out.Rows
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 2 || columns[0] != "id" || columns[1] != second {
		t.Fatal("DELETE RETURNING columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	wantType := "TEXT"
	if second == "matched" {
		wantType = "BOOL"
	}
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != wantType {
		t.Fatal("DELETE RETURNING types", types, err)
	}
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		if second == "matched" {
			var matched bool
			if err := rows.Scan(&id, &matched); err != nil || !matched {
				t.Fatal("DELETE RETURNING predicate", id, matched, err)
			}
		} else {
			var value string
			if err := rows.Scan(&id, &value); err != nil || value != map[int64]string{1: "alpha", 3: "alpine"}[id] {
				t.Fatal("DELETE RETURNING value", id, value, err)
			}
		}
		if id != 1 && id != 3 || seen[id] {
			t.Fatal("DELETE RETURNING unexpected/duplicate identity", id)
		}
		seen[id] = true
	}
	if rows.Err() != nil || len(seen) != 2 {
		t.Fatal("DELETE RETURNING cardinality", seen, rows.Err())
	}
}
