package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteActiveCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE TABLE jbcd_active_input(id int,doc jsonb,rhs jsonb,key text,idx int,saved jsonb)", 0))
	run(fixture.Exec("CREATE TABLE jbcd_active_out(id int,v jsonb)", 0))
	for id := 1; id <= 2; id++ {
		run(fixture.Exec(`INSERT INTO jbcd_active_input(id,doc,rhs,key,idx) VALUES($1,'["drop",1]','[2]','drop',0)`, 1, id))
	}
	// Parse one substantial immutable operand once, then reuse stored values.
	// Avoid repeated large binds and redundant heavy rows in each phase.
	// Row 1 remains an early valid write; row 2 supplies the admission window.
	run(fixture.Prepared(fixture.Exec("UPDATE jbcd_active_input SET saved=$1 WHERE id=2", 1,
		`["drop","`+strings.Repeat("x", 32<<20)+`"]`)))
	for _, tc := range []struct{ name, expression, provider, want string }{
		{"concat", "doc || rhs", "sqltype.BinaryPlan.jsonbConcat", `["drop", 1, 2]`},
		{"text", "doc - key", "sqltype.BinaryPlan.jsonbDelete", `[1]`},
		{"integer", "doc - idx", "sqltype.BinaryPlan.jsonbDelete", `[1]`},
	} {
		t.Run("active_"+tc.name, func(t *testing.T) {
			run(fixture.Exec("INSERT INTO jbcd_active_out VALUES(0,'99')", 1))
			readSQL := "SELECT sum(length((" + tc.expression + ")::text)) AS n FROM jbcd_active_input"
			writeSQL := "INSERT INTO jbcd_active_out SELECT id," + tc.expression + " FROM jbcd_active_input ORDER BY id"
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
			// All input conversion finishes before admission. Both operators must
			// publish a changed root, retaining the large subtree for rendering.
			run(fixture.Exec("UPDATE jbcd_active_input SET doc=saved WHERE id=2", 1))
			fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, tc.provider, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := read.QueryContext(ctx)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			run(fixture.Exec(`UPDATE jbcd_active_input SET doc='["drop",1]' WHERE id=2`, 1))
			held := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(2 * len(tc.want))}}))
			run(fixture.Exec("UPDATE jbcd_active_input SET doc=saved WHERE id=2", 1))
			fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, tc.provider, func(ctx context.Context) fixture.RelationOutcome {
				result, err := write.ExecContext(ctx)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			run(fixture.Select("SELECT id,v FROM jbcd_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), "99"}}))
			run(jsonbConcatDeleteSelect("SELECT doc AS v FROM jbcd_active_input WHERE id=1", `["drop", 1]`))
			run(fixture.Exec(`UPDATE jbcd_active_input SET doc='["drop",1]' WHERE id=2`, 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(2 * len(tc.want))}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 2))
			want := [][]any{{int64(0), "99"}}
			for id := int64(1); id <= 2; id++ {
				want = append(want, []any{id, tc.want})
			}
			run(fixture.Select("SELECT id,v FROM jbcd_active_out ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, want))
			run(fixture.Exec("DELETE FROM jbcd_active_out", 3))
		})
	}
}
