package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsCancellation(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE jbe_active_input(id int,j jsonb)", 0))
		run(fixture.Exec(`INSERT INTO jbe_active_input VALUES(1,'[1]'),(2,'[1]'),(3,'[1]'),(4,'[1]'),(5,'[1]'),(6,'[1]'),(7,'[1]'),(8,'[1]'),(9,'[1]')`, 9))
		// Import before admission. Each element is a nested container, so both
		// result modes must render its large string rather than borrow TEXT.
		large := `[{"nested":"` + strings.Repeat("x", 32<<20) + `"}]`
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			dest := "jbe_cancel_" + f
			run(fixture.Exec("CREATE TABLE "+dest+"(id int,v "+jsonbElementsType(f)+")", 0))
			run(fixture.Exec("INSERT INTO "+dest+" VALUES(0,'99')", 1))
			readSQL := "SELECT sum(length(e.value::text)) AS n FROM jbe_active_input s," + f + "(s.j) e"
			writeSQL := "INSERT INTO " + dest + " SELECT s.id,e.value FROM jbe_active_input s," + f + "(s.j) e ORDER BY s.id"
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
			for _, writing := range []bool{false, true} {
				run(fixture.Prepared(fixture.Exec("UPDATE jbe_active_input SET j=$1 WHERE id>1", 8, large)))
				query := readSQL
				if writing {
					query = writeSQL
				}
				fixture.BinaryConversionActiveCancel(t, p, b, c, query, stuntgres.WorkJSONBArrayExpand, func(ctx context.Context) fixture.RelationOutcome {
					if writing {
						r, err := writer.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					}
					r, err := reader.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: r, Err: err}
				})
				run(fixture.Select("SELECT id,v FROM "+dest, []string{"id", "v"}, []string{"INT4", jsonbElementsType(f)}, [][]any{{int64(0), "99"}}))
				run(fixture.Exec(`UPDATE jbe_active_input SET j='[1]' WHERE id>1`, 8))
				jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: reader, SQL: readSQL}, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
			}
			jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: writer, SQL: writeSQL}, fixture.Exec(writeSQL, 9))
			want := [][]any{{int64(0), "99"}}
			for id := int64(1); id <= 9; id++ {
				want = append(want, []any{id, "1"})
			}
			run(fixture.Select("SELECT id,v FROM "+dest+" ORDER BY id", []string{"id", "v"}, []string{"INT4", jsonbElementsType(f)}, want))
		}
	})
}
