package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExecutionPause(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// A host request is dormant during locked metadata binding, even when
		// immutable integer-to-regtype capture actually evaluates this builtin.
		metadataContext := p.Ctx
		var metadataPause *stuntgres.ExecutionPause
		if !b.PG {
			metadataPause = stuntgres.NewExecutionPause("json.array_length")
			defer metadataPause.Resume()
			metadataContext = stuntgres.WithExecutionPause(metadataContext, metadataPause)
		}
		array := "[" + strings.Repeat("0,", 22) + "0]"
		fixture.RunStep(t, metadataContext, c, b.Recorder, fixture.Exec("CREATE TABLE execution_pause_metadata(v regtype DEFAULT json_array_length('"+array+"')::regtype)", 0))
		description, err := c.PrepareContext(metadataContext, "SELECT json_array_length('"+array+"')::regtype")
		if err != nil {
			t.Fatal("unsuspended description", err)
		}
		description.Close()
		if metadataPause != nil {
			metadataPause.Resume()
			if err := metadataPause.Wait(p.Ctx); err == nil {
				t.Fatal("binding incorrectly admitted detached work")
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO execution_pause_metadata DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v::oid::bigint AS v FROM execution_pause_metadata", []string{"v"}, []string{"INT8"}, [][]any{{int64(23)}}))
		const query = "SELECT key,value::text AS value FROM jsonb_each($1::jsonb) ORDER BY key"
		const document = `{"b":2,"a":1}`
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
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
			rows, err := statement.QueryContext(ctx, document)
			done <- fixture.RelationOutcome{Rows: rows, Err: err}
		}()
		if pause != nil {
			if err := pause.Wait(p.Ctx); err != nil {
				t.Fatal("provider admission", err)
			}
			// The paused provider cannot retain an engine/session mutex: an
			// unrelated physical owner still executes against this same engine.
			var value int
			if err := p.Native.QueryRowContext(p.Ctx, "SELECT 7").Scan(&value); err != nil || value != 7 {
				t.Fatal("unrelated owner blocked", value, err)
			}
			pause.Resume()
		}
		out := <-done
		joined = true
		if out.Err != nil || out.Rows == nil {
			t.Fatal("resumed query", out.Err)
		}
		rows := out.Rows
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || len(columns) != 2 || columns[0] != "key" || columns[1] != "value" {
			t.Fatal("resumed columns", columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("resumed types", types, err)
		}
		for _, want := range [][2]string{{"a", "1"}, {"b", "2"}} {
			if !rows.Next() {
				t.Fatal("missing resumed row", rows.Err())
			}
			var key, value string
			if err := rows.Scan(&key, &value); err != nil || key != want[0] || value != want[1] {
				t.Fatal("resumed row", key, value, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("unexpected resumed rows", rows.Err())
		}
		rows.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "1"}, {"b", "2"}}, document))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE execution_pause_sink(k text,v text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO execution_pause_sink VALUES('sentinel','9')", 1))
		const insert = "INSERT INTO execution_pause_sink SELECT key,value::text FROM jsonb_each($1::jsonb)"
		write, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		if !b.PG {
			executionPauseCanceledWrite(t, p, write, document)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT k,v FROM execution_pause_sink", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"sentinel", "9"}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: insert}, b.Recorder, fixture.Exec(insert, 2, document))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT k,v FROM execution_pause_sink ORDER BY k", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "1"}, {"b", "2"}, {"sentinel", "9"}}))
	})
}

func executionPauseCanceledWrite(t *testing.T, p *fixture.Pair, statement *sql.Stmt, document string) {
	t.Helper()
	pause := stuntgres.NewExecutionPause("json.each.next")
	defer pause.Resume()
	ctx, cancel := context.WithCancel(stuntgres.WithExecutionPause(p.Ctx, pause))
	defer cancel()
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	go func() {
		result, err := statement.ExecContext(ctx, document)
		done <- fixture.RelationOutcome{Result: result, Err: err}
	}()
	if err := pause.Wait(p.Ctx); err != nil {
		t.Fatal("write admission", err)
	}
	cancel()
	out := <-done
	joined = true
	if out.Result != nil || out.Rows != nil {
		t.Fatal("canceled write published output")
	}
	fixture.LexicalOrdinary(t, out.Err, context.Canceled)
}
