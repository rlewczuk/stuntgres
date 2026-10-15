package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsCancellation(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE js_cancel_input(id int,v json)", 0))
		run(fixture.Exec("CREATE TABLE js_cancel_output(id int,v json)", 0))
		run(fixture.Exec("INSERT INTO js_cancel_output VALUES(0,'99')", 1))
		run(fixture.Exec("INSERT INTO js_cancel_input VALUES(1,'[null,1]'),(2,'[null,1]'),(3,'[null,1]'),(4,'[null,1]'),(5,'[null,1]'),(6,'[null,1]'),(7,'[null,1]'),(8,'[null,1]'),(9,'[null,1]')", 9))
		// Successful shared workloads cover depth, wide commas, large numeric spans
		// and escaping independently of backend-specific cancellation admission.
		for _, tc := range []struct{ input, want string }{
			{strings.Repeat("[", 128) + "null" + strings.Repeat("]", 128), strings.Repeat("[", 128) + strings.Repeat("]", 128)},
			{"[" + strings.Repeat("null,", 4096) + "1]", "[1]"},
			{"1" + strings.Repeat("0", 65536), "1" + strings.Repeat("0", 65536)},
			{`"` + strings.Repeat(`\n`, 65536) + `"`, `"` + strings.Repeat(`\n`, 65536) + `"`},
		} {
			run(fixture.Prepared(jsonStripSelect("SELECT json_strip_nulls($1,true) AS stripped", tc.want, tc.input)))
		}
		large := `{"drop":null,"wide":[` + strings.Repeat("null,", 1<<16) + `"` + strings.Repeat("x", 32<<20) + `"]}`
		run(fixture.Prepared(fixture.Exec("UPDATE js_cancel_input SET v=$1 WHERE id>1", 8, large)))
		readSQL := "SELECT sum(length(json_strip_nulls(v,true)::text)) AS n FROM js_cancel_input"
		writeSQL := "INSERT INTO js_cancel_output SELECT id,json_strip_nulls(v,true) FROM js_cancel_input ORDER BY id"
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
		fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, "sqltype.JSONStripNullsPlan.Evaluate", func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("UPDATE js_cancel_input SET v='[null,1]' WHERE id>1", 8))
		jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(27)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE js_cancel_input SET v=$1 WHERE id>1", 8, large)))
		fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, "sqltype.JSONStripNullsPlan.Evaluate", func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(fixture.Select("SELECT id,v FROM js_cancel_output ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(0), "99"}}))
		run(fixture.Exec("UPDATE js_cancel_input SET v='[null,1]' WHERE id>1", 8))
		jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, fixture.Exec(writeSQL, 9))
		want := [][]any{{int64(0), "99"}}
		for id := int64(1); id <= 9; id++ {
			want = append(want, []any{id, "[1]"})
		}
		run(fixture.Select("SELECT id,v FROM js_cancel_output ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, want))
	})
}
