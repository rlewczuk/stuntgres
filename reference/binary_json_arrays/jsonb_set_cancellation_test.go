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
		run(fixture.Exec(`CREATE TABLE jbs_active_out(id int,v jsonb,
			CHECK(pg_advisory_xact_lock(CASE WHEN length(v::text)>6 THEN 67108864::bigint ELSE 2::bigint END) IS NOT NULL))`, 0))
		run(fixture.Exec("INSERT INTO jbs_active_out VALUES(0,'99')", 1))
		run(fixture.Exec("INSERT INTO jbs_active_input VALUES(1,'[0]'),(2,'[0]'),(3,'[0]'),(4,'[0]'),(5,'[0]'),(6,'[0]'),(7,'[0]'),(8,'[0]'),(9,'[0]')", 9))
		// Keep the bounded typed payload: admission depends on completed
		// jsonb_set output, not catching a transient mutation-provider frame.
		large := "[" + strings.Repeat("0,", 1<<16) + `"` + strings.Repeat("x", 32<<20) + `"]`
		run(fixture.Prepared(fixture.Exec("UPDATE jbs_active_input SET v=$1 WHERE id>1", 8, large)))
		readSQL := `WITH produced AS MATERIALIZED (
			SELECT jsonb_set(v,'{0}','9') AS v FROM jbs_active_input
		), gated AS MATERIALIZED (
			SELECT v,pg_advisory_xact_lock(CASE WHEN length(v::text)>6 THEN 67108864::bigint ELSE 2::bigint END) AS gate FROM produced
		) SELECT sum(length(v::text)) AS n FROM gated`
		// The destination CHECK gates actual output after the ordered first
		// small row; require its grant before canceling the large output.
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
			for _, route := range []struct {
				name        string
				read, write fixture.Executor
			}{
				{"direct", c, c},
				{"held", fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}},
			} {
				rows, err := route.read.QueryContext(ctx, readSQL)
				if rows != nil {
					rows.Close()
					t.Fatal(route.name, "precanceled read published cursor")
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
				result, err := route.write.ExecContext(ctx, writeSQL)
				if result != nil {
					t.Fatal(route.name, "precanceled write published result")
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
			}
		}
		fixture.OutputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("UPDATE jbs_active_input SET v='[0]' WHERE id>1", 8))
		heldRead := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(27)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE jbs_active_input SET v=$1 WHERE id>1", 8, large)))
		fixture.OutputGateCancel(t, p, b, c, true, func(ctx context.Context) fixture.RelationOutcome {
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
