package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONElementsCancellation(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jelements_active(id int,j json)", 0))
		run(fixture.Exec(`INSERT INTO jelements_active VALUES(1,'[1]'),(2,'[1]'),(3,'[1]'),(4,'[1]'),(5,'[1]'),(6,'[1]'),(7,'[1]'),(8,'[1]'),(9,'[1]')`, 9))
		large := `[{"nested":"` + strings.Repeat("x", 32<<20) + `"}]`
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			t.Run(f, func(t *testing.T) {
				dest := "jelements_cancel_" + f
				run(fixture.Exec("CREATE TABLE "+dest+"(id int,v "+jsonElementsType(f)+")", 0))
				run(fixture.Exec("INSERT INTO "+dest+" VALUES(0,'99')", 1))
				readSQL := "SELECT sum(length(e.value::text)) AS n FROM jelements_active s," + f + "(s.j) e"
				writeSQL := "INSERT INTO " + dest + " SELECT s.id,e.value FROM jelements_active s," + f + "(s.j) e ORDER BY s.id"
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
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := error(context.Canceled)
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(1, 0))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						for _, held := range []bool{false, true} {
							var rows *sql.Rows
							var err error
							var result sql.Result
							if held {
								rows, err = read.QueryContext(ctx)
							} else {
								rows, err = c.QueryContext(ctx, readSQL)
							}
							if rows != nil {
								rows.Close()
								t.Fatal("canceled read published rows")
							}
							fixture.LexicalOrdinary(t, err, want)
							if held {
								result, err = write.ExecContext(ctx)
							} else {
								result, err = c.ExecContext(ctx, writeSQL)
							}
							if result != nil {
								t.Fatal("canceled write published result")
							}
							fixture.LexicalOrdinary(t, err, want)
						}
						cancel()
					}
				}
				for _, writing := range []bool{false, true} {
					run(fixture.Prepared(fixture.Exec("UPDATE jelements_active SET j=$1 WHERE id>1", 8, large)))
					q := readSQL
					if writing {
						q = writeSQL
					}
					fixture.BinaryConversionActiveCancel(t, p, b, c, q, "sqltype.JSONElementsPlan.Open", func(ctx context.Context) fixture.RelationOutcome {
						if writing {
							result, err := write.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						rows, err := read.QueryContext(ctx)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					run(fixture.Select("SELECT id,v FROM "+dest, []string{"id", "v"}, []string{"INT4", jsonElementsType(f)}, [][]any{{int64(0), "99"}}))
					run(fixture.Exec(`UPDATE jelements_active SET j='[1]' WHERE id>1`, 8))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
			})
		}
	})
}
