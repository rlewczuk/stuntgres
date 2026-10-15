package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPathDeleteCancellation(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE jbd_active_input(id int,doc jsonb,path text[])", 0))
		run(fixture.Exec("CREATE TABLE jbd_active_out(id int,v jsonb)", 0))
		run(fixture.Exec("INSERT INTO jbd_active_out VALUES(0,'99')", 1))
		run(fixture.Exec("INSERT INTO jbd_active_input VALUES(1,'[0]','{0}'),(2,'[0]','{0}'),(3,'[0]','{0}'),(4,'[0]','{0}'),(5,'[0]','{0}'),(6,'[0]','{0}'),(7,'[0]','{0}'),(8,'[0]','{0}'),(9,'[0]','{0}')", 9))
		// Stored operands: admission/cancellation exercises removal and output,
		// not JSON or TEXT[] input conversion.
		large := "[" + strings.Repeat("0,", 1<<16) + `"` + strings.Repeat("x", 32<<20) + `"]`
		run(fixture.Prepared(fixture.Exec("UPDATE jbd_active_input SET doc=$1 WHERE id>1", 8, large)))
		readSQL := "SELECT sum(length((doc #- path)::text)) AS n FROM jbd_active_input"
		writeSQL := "INSERT INTO jbd_active_out(id,v) SELECT id,doc #- path FROM jbd_active_input ORDER BY id"
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
			rows, err := c.QueryContext(ctx, readSQL)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled direct read published cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			rows, err = read.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled held read published cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			result, err := write.ExecContext(ctx)
			if result != nil {
				t.Fatal("precanceled write published result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		// Keep admission inside the deletion provider through both the short
		// removal walk and canonical output of the retained 32MiB subtree.
		// Matching only jsonbSetPath misses the dominant output phase.
		provider := "sqltype.BinaryPlan.jsonbPathDelete"
		fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, provider, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("UPDATE jbd_active_input SET doc='[0]' WHERE id>1", 8))
		heldRead := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(18)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE jbd_active_input SET doc=$1 WHERE id>1", 8, large)))
		fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, provider, func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(fixture.Select("SELECT id,v FROM jbd_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), "99"}}))
		run(jsonbPathDeleteSelect("SELECT doc AS v FROM jbd_active_input WHERE id=1", "[0]"))
		run(fixture.Exec("UPDATE jbd_active_input SET doc='[0]' WHERE id>1", 8))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(18)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
		want := [][]any{{int64(0), "99"}}
		for id := int64(1); id <= 9; id++ {
			want = append(want, []any{id, "[]"})
		}
		run(fixture.Select("SELECT id,v FROM jbd_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, want))
	})
}
