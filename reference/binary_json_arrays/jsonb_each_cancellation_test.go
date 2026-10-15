package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachCancellation(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jbeach_active_input(id int,j jsonb)", 0))
		run(fixture.Exec(`INSERT INTO jbeach_active_input VALUES(1,'{"a":1}'),(2,'{"a":1}'),(3,'{"a":1}'),(4,'{"a":1}'),(5,'{"a":1}'),(6,'{"a":1}'),(7,'{"a":1}'),(8,'{"a":1}'),(9,'{"a":1}')`, 9))
		// Store before measuring admission: cancellation witnesses processing,
		// not merely the adapter's JSON parameter import. Validate long nested
		// strings even when each returns the raw object span.
		large := `{"a":{"nested":"` + strings.Repeat("x", 32<<20) + `"}}`
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			t.Run(f, func(t *testing.T) {
				dest := "jbeach_cancel_" + f
				run(fixture.Exec("CREATE TABLE "+dest+"(id int,k text,v "+jsonbEachType(f)+")", 0))
				run(fixture.Exec("INSERT INTO "+dest+" VALUES(0,'sentinel','99')", 1))
				readSQL := "SELECT sum(length(f.value::text)) AS n FROM jbeach_active_input s," + f + "(s.j) f"
				writeSQL := "INSERT INTO " + dest + " SELECT s.id,f.key,f.value FROM jbeach_active_input s," + f + "(s.j) f ORDER BY s.id"
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
				for _, writing := range []bool{false, true} {
					run(fixture.Prepared(fixture.Exec("UPDATE jbeach_active_input SET j=$1 WHERE id>1", 8, large)))
					query := readSQL
					if writing {
						query = writeSQL
					}
					fixture.BinaryConversionActiveCancel(t, p, b, c, query, "sqltype.(*JSONEachCursor).Next", func(ctx context.Context) fixture.RelationOutcome {
						if writing {
							result, err := write.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						rows, err := read.QueryContext(ctx)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					run(fixture.Select("SELECT id,k,v FROM "+dest, []string{"id", "k", "v"}, []string{"INT4", "TEXT", jsonbEachType(f)}, [][]any{{int64(0), "sentinel", "99"}}))
					run(fixture.Exec(`UPDATE jbeach_active_input SET j='{"a":1}' WHERE id>1`, 8))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
				want := [][]any{{int64(0), "sentinel", "99"}}
				for id := int64(1); id <= 9; id++ {
					want = append(want, []any{id, "a", "1"})
				}
				run(fixture.Select("SELECT id,k,v FROM "+dest+" ORDER BY id", []string{"id", "k", "v"}, []string{"INT4", "TEXT", jsonbEachType(f)}, want))
			})
		}
	})
}
