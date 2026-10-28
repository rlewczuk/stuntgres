package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordCancellation(t *testing.T) {
	for _, function := range []string{"json_populate_record", "json_populate_recordset", "json_to_record", "json_to_recordset"} {
		t.Run(function, func(t *testing.T) {
			jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				set := strings.HasSuffix(function, "recordset")
				populate := strings.HasPrefix(function, "json_populate")
				small := `{"a":1,"b":"x"}`
				large := `{"a":1,"b":"` + strings.Repeat("x", 32<<20) + `"}`
				if set {
					small, large = "["+small+"]", "["+large+"]"
				}
				run(fixture.Exec(`CREATE TABLE jr_active_input(id int,j json)`, 0))
				for id := 1; id <= 9; id++ {
					run(fixture.Exec(`INSERT INTO jr_active_input VALUES($1,$2)`, 1, id, small))
				}
				run(fixture.Exec(`CREATE TABLE jr_cancel_destination(id int,a int,b text)`, 0))
				run(fixture.Exec(`INSERT INTO jr_cancel_destination VALUES(0,99,'sentinel')`, 1))
				source := function + `(s.j) AS r(a int,b text)`
				if populate {
					source = function + `(NULL::jr_pair,s.j) AS r`
				}
				readSQL := `SELECT sum(length(r.b)) AS n FROM jr_active_input s,` + source
				writeSQL := `INSERT INTO jr_cancel_destination SELECT s.id,r.a,r.b FROM jr_active_input s,` + source + ` ORDER BY s.id`
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
							ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						rows, err := c.QueryContext(ctx, readSQL)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled direct query published rows")
						}
						fixture.LexicalOrdinary(t, err, want)
						result, err := write.ExecContext(ctx)
						cancel()
						if result != nil {
							t.Fatal("canceled held insert published result")
						}
						fixture.LexicalOrdinary(t, err, want)
					}
				}
				for _, writing := range []bool{false, true} {
					// Store the operand before admission: observe JSON record
					// traversal, not public parameter import or fixture construction.
					run(fixture.Prepared(fixture.Exec(`UPDATE jr_active_input SET j=$1 WHERE id>1`, 8, large)))
					query := readSQL
					if writing {
						query = writeSQL
					}
					fixture.BinaryConversionActiveCancel(t, p, b, c, query, stuntgres.WorkJSONRecordExpand, func(ctx context.Context) fixture.RelationOutcome {
						if writing {
							result, err := write.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						rows, err := read.QueryContext(ctx)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					run(fixture.Select(`SELECT id,a,b FROM jr_cancel_destination`, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(0), int64(99), "sentinel"}}))
					run(fixture.Exec(`UPDATE jr_active_input SET j=$1 WHERE id>1`, 8, small))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
				want := [][]any{{int64(0), int64(99), "sentinel"}}
				for id := int64(1); id <= 9; id++ {
					want = append(want, []any{id, int64(1), "x"})
				}
				run(fixture.Select(`SELECT id,a,b FROM jr_cancel_destination ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, want))
			})
		})
	}
}
