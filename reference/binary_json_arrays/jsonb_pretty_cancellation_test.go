package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPrettyCancellation(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE jbp_active_input(id int,j jsonb)", 0))
		run(fixture.Exec("INSERT INTO jbp_active_input VALUES(1,'1'),(2,'1'),(3,'1'),(4,'1'),(5,'1'),(6,'1'),(7,'1'),(8,'1'),(9,'1')", 9))
		run(fixture.Exec("CREATE TABLE jbp_cancel_dest(id int,v text)", 0))
		run(fixture.Exec("INSERT INTO jbp_cancel_dest VALUES(0,'99')", 1))
		const readSQL = "SELECT sum(length(jsonb_pretty(j))) AS n FROM jbp_active_input"
		const writeSQL = "INSERT INTO jbp_cancel_dest SELECT id,jsonb_pretty(j) FROM jbp_active_input ORDER BY id"
		reader, err := c.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		writer, err := c.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			stmt, err := c.PrepareContext(ctx, readSQL)
			if stmt != nil {
				stmt.Close()
				t.Fatal("precanceled prepare published handle")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			rows, err := reader.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled read published rows")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			result, err := writer.ExecContext(ctx)
			if result != nil {
				t.Fatal("precanceled write published result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		// Import outside the operation: cancellation must reach the actual renderer,
		// not merely a parameter decoder. Both key and value require long writes.
		large := `[{"` + strings.Repeat("k", 1<<20) + `":"` + strings.Repeat("x", 32<<20) + `"}]`
		for _, writing := range []bool{false, true} {
			run(fixture.Prepared(fixture.Exec("UPDATE jbp_active_input SET j=$1 WHERE id>1", 8, large)))
			query := readSQL
			if writing {
				query = writeSQL
			}
			fixture.BinaryConversionActiveCancel(t, p, b, c, query, "jsonbPrettyOutput", func(ctx context.Context) fixture.RelationOutcome {
				if writing {
					r, err := writer.ExecContext(ctx)
					return fixture.RelationOutcome{Result: r, Err: err}
				}
				r, err := reader.QueryContext(ctx)
				return fixture.RelationOutcome{Rows: r, Err: err}
			})
			run(fixture.Select("SELECT id,v FROM jbp_cancel_dest ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "99"}}))
			run(fixture.Exec("UPDATE jbp_active_input SET j='1' WHERE id>1", 8))
			jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: reader, SQL: readSQL}, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
		}
		jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: writer, SQL: writeSQL}, fixture.Exec(writeSQL, 9))
		want := [][]any{{int64(0), "99"}}
		for id := int64(1); id <= 9; id++ {
			want = append(want, []any{id, "1"})
		}
		run(fixture.Select("SELECT id,v FROM jbp_cancel_dest ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, want))
	})
}
