package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExecutionPauseCursor(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
				t.Error("cursor transaction cleanup", err)
			}
		}()
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		const declaration = `DECLARE execution_pause_cursor NO SCROLL CURSOR FOR SELECT key,value::text AS value FROM jsonb_each('{"a":1,"b":2}'::jsonb)`
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(declaration, 0))
		executionPauseCursorFetch(t, p, b, c, "FETCH FORWARD 1 FROM execution_pause_cursor", [][2]string{{"a", "1"}})
		// The second request must admit CURRENT demand on the retained stage,
		// rather than reusing the first FETCH's context or only opening context.
		executionPauseCursorFetch(t, p, b, c, "FETCH ALL FROM execution_pause_cursor", [][2]string{{"b", "2"}})
		run(fixture.Exec("CLOSE execution_pause_cursor", 0))
		run(fixture.Exec("COMMIT", 0))

		// WITH HOLD promotion demands the remaining producer under the current
		// COMMIT operation. Later FETCH reads owned stored rows, not the provider.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(`DECLARE execution_pause_hold NO SCROLL CURSOR WITH HOLD FOR SELECT key,value::text AS value FROM jsonb_each('{"a":1,"b":2,"c":3}'::jsonb)`, 0))
		executionPauseCursorFetch(t, p, b, c, "FETCH FORWARD 1 FROM execution_pause_hold", [][2]string{{"a", "1"}})
		executionPauseCursorDemand(t, p, b, c, "COMMIT", true, func(out fixture.RelationOutcome) {
			if out.Err != nil || out.Result == nil || out.Rows != nil {
				t.Fatal("paused HOLD COMMIT", out.Err)
			}
			count, err := out.Result.RowsAffected()
			if err != nil || count != 0 {
				t.Fatal("HOLD COMMIT count", count, err)
			}
		})
		run(fixture.Select("FETCH ALL FROM execution_pause_hold", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "2"}, {"c", "3"}}))
		run(fixture.Exec("CLOSE execution_pause_hold", 0))

		// A real lock wait keeps the server-side cancellation witness live with
		// a fixed two-row dataset. Native admission still targets actual Next,
		// before this expression can acquire its transaction lock.
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		const key = int64(64018327)
		if _, err := holder.ExecContext(p.Ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var unlocked bool
			if err := holder.QueryRowContext(cleanup, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil || !unlocked {
				t.Error("cursor cancellation lock cleanup", unlocked, err)
			}
		}()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(`DECLARE execution_pause_cancel NO SCROLL CURSOR FOR SELECT key,value::text AS value,pg_advisory_xact_lock(64018327) AS held FROM jsonb_each('{"a":1,"b":2}'::jsonb)`, 0))
		const fetch = "FETCH ALL FROM execution_pause_cancel"
		held, err := c.PrepareContext(p.Ctx, fetch)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		fixture.BinaryConversionActiveCancel(t, p, b, c, fetch, stuntgres.WorkJSONBObjectExpand, func(ctx context.Context) fixture.RelationOutcome {
			result, err := held.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		held.Close()
		// User cancellation aborts PostgreSQL's transaction. Recovery is a
		// full rollback, never reuse of an aborted cursor or fabricated retry.
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(declaration, 0))
		executionPauseCursorFetch(t, p, b, c, "FETCH ALL FROM execution_pause_cursor", [][2]string{{"a", "1"}, {"b", "2"}})
		run(fixture.Exec("CLOSE execution_pause_cursor", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT 11 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}

func executionPauseCursorFetch(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want [][2]string) {
	t.Helper()
	executionPauseCursorDemand(t, p, b, c, query, false, func(out fixture.RelationOutcome) {
		if out.Err != nil || out.Rows == nil {
			t.Fatal("cursor normal Resume", query, out.Err)
		}
		rows := out.Rows
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || len(columns) != 2 || columns[0] != "key" || columns[1] != "value" {
			t.Fatal("cursor columns", columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("cursor types", types, err)
		}
		for _, pair := range want {
			if !rows.Next() {
				t.Fatal("missing cursor row", query, rows.Err())
			}
			var key, value string
			if err := rows.Scan(&key, &value); err != nil || key != pair[0] || value != pair[1] {
				t.Fatal("cursor row", key, value, pair, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("unexpected cursor rows", query, rows.Err())
		}
	})
}

func executionPauseCursorDemand(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, execute bool, consume func(fixture.RelationOutcome)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	var pause *stuntgres.ExecutionPause
	if !b.PG {
		pause = stuntgres.NewExecutionPause("json.each.next")
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
	go func() {
		if execute {
			result, err := c.ExecContext(ctx, query)
			done <- fixture.RelationOutcome{Result: result, Err: err}
			return
		}
		rows, err := c.QueryContext(ctx, query)
		done <- fixture.RelationOutcome{Rows: rows, Err: err}
	}()
	if pause != nil {
		admitted := make(chan error, 1)
		observe, stop := context.WithCancel(ctx)
		go func() { admitted <- pause.Wait(observe) }()
		select {
		case err := <-admitted:
			stop()
			if err != nil {
				t.Fatal("cursor provider admission", query, err)
			}
		case out := <-done:
			joined = true
			stop()
			<-admitted
			if out.Rows != nil {
				out.Rows.Close()
			}
			t.Fatal("cursor demand completed before provider admission", query, out.Err)
		}
		var other int
		if err := p.Native.QueryRowContext(ctx, "SELECT 7").Scan(&other); err != nil || other != 7 {
			t.Fatal("paused cursor blocked independent owner", other, err)
		}
		pause.Resume()
	}
	out := <-done
	joined = true
	consume(out)
}
