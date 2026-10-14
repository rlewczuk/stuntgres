package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBSetCancellation(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jbs_active_input(id int,v jsonb)", 0))
		run(fixture.Exec("CREATE TABLE jbs_active_out(id int,v jsonb)", 0))
		run(fixture.Exec("INSERT INTO jbs_active_out VALUES(0,'99')", 1))
		run(fixture.Exec("INSERT INTO jbs_active_input VALUES(1,'[0]'),(2,'[0]'),(3,'[0]'),(4,'[0]'),(5,'[0]'),(6,'[0]'),(7,'[0]'),(8,'[0]'),(9,'[0]')", 9))
		// Import once per phase. Replacement copies a wide typed ancestor and
		// outputs a retained large subtree, not a JSON input lexer workload.
		large := "[" + strings.Repeat("0,", 1<<16) + `"` + strings.Repeat("x", 32<<20) + `"]`
		run(fixture.Prepared(fixture.Exec("UPDATE jbs_active_input SET v=$1 WHERE id>1", 8, large)))
		readSQL := "SELECT sum(length(jsonb_set(v,'{0}','9')::text)) AS n FROM jbs_active_input"
		writeSQL := "INSERT INTO jbs_active_out(id,v) SELECT id,jsonb_set(v,'{0}','9') FROM jbs_active_input ORDER BY id"
		read, err := c.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := c.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := read.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled read published cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			result, err := write.ExecContext(ctx)
			if result != nil {
				t.Fatal("precanceled write published result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		// Observe the actual value-receiver typed provider through traversal,
		// ancestor copying and canonical output, never an input lexer frame.
		provider := "sqltype.JSONBSetPlan.Evaluate"
		fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, provider, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("UPDATE jbs_active_input SET v='[0]' WHERE id>1", 8))
		heldRead := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(27)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE jbs_active_input SET v=$1 WHERE id>1", 8, large)))
		fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, provider, func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(fixture.Select("SELECT id,v FROM jbs_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), "99"}}))
		run(fixture.Select("SELECT v AS changed FROM jbs_active_input WHERE id=1", []string{"changed"}, []string{"JSONB"}, [][]any{{"[0]"}}))
		run(fixture.Exec("UPDATE jbs_active_input SET v='[0]' WHERE id>1", 8))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(27)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
		want := [][]any{{int64(0), "99"}}
		for id := int64(1); id <= 9; id++ {
			want = append(want, []any{id, "[9]"})
		}
		run(fixture.Select("SELECT id,v FROM jbs_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, want))
	})
}
