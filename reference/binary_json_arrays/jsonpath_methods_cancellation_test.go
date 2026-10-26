package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathMethodsCancellation(t *testing.T) {
	for _, method := range []string{"string", "keyvalue"} {
		t.Run(method, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpm_active(id int,v jsonb,p jsonpath,vars jsonb DEFAULT '{}')`, 0))
				run(fixture.Exec(`CREATE TABLE jpm_cancel_out(id int,v jsonb,
					CHECK(pg_advisory_xact_lock(CASE WHEN length(v::text)>5 THEN 67108864::bigint ELSE 2::bigint END) IS NOT NULL))`, 0))
				run(fixture.Exec(`INSERT INTO jpm_active(id,v,p) VALUES(1,'[0]','$'),(2,'[0]','$'),(3,'[0]','$'),(4,'[0]','$'),(5,'[0]','$')`, 5))
				run(fixture.Exec(`INSERT INTO jpm_cancel_out VALUES(0,'99')`, 1))
				element := `"` + strings.Repeat("x", 4080) + `"`
				small := `["x"]`
				path := `$[*].string()`
				want := `["x"]`
				if method == "keyvalue" {
					element = `{"a":` + element + `}`
					small = `[{"a":1}]`
					path = `$[*].keyvalue().value.string()`
					want = `["1"]`
				}
				large := `[` + strings.Repeat(element+",", 8191) + element + `]`
				// Keep the bounded stored payload: admission depends on completed
				// selected-method output, never on catching a transient private frame.
				run(fixture.Prepared(fixture.Exec(`UPDATE jpm_active SET v=$1,p=$2 WHERE id=1`, 1, small, path)))
				call := `jsonb_path_query_array(v,p,vars,false)`
				readSQL := `WITH produced AS MATERIALIZED (
					SELECT ` + call + ` AS v FROM jpm_active
				), gated AS MATERIALIZED (
					SELECT v,pg_advisory_xact_lock(CASE WHEN length(v::text)>5 THEN 67108864::bigint ELSE 2::bigint END) AS gate FROM produced
				) SELECT sum(length(v::text)) AS n FROM gated`
				// The destination CHECK gates actual output after the ordered first
				// small row. Its grant must be visible before canceling the large row.
				writeSQL := `INSERT INTO jpm_cancel_out SELECT id,` + call + ` FROM jpm_active ORDER BY id`
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
							t.Fatal(route.name, "precanceled read published rows")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
						result, err := route.write.ExecContext(ctx, writeSQL)
						if result != nil {
							t.Fatal(route.name, "precanceled write published result")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
					}
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, `UPDATE jpm_active SET v='[0]',p='$'`); err != nil {
						t.Error(err)
					}
				}()
				// Typed inputs avoid measuring parsing. The persistent lock wait
				// proves a large string/keyvalue result has actually materialized.
				run(fixture.Prepared(fixture.Exec(`UPDATE jpm_active SET v=$1,p=$2 WHERE id>1`, 4, large, path)))
				outputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Prepared(fixture.Exec(`UPDATE jpm_active SET v=$1,p=$2 WHERE id>1`, 4, small, path)))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(25)}}))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpm_active SET v=$1,p=$2 WHERE id>1`, 4, large, path)))
				outputGateCancel(t, p, b, c, true, func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpm_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpm_active SET v=$1,p=$2 WHERE id>1`, 4, small, path)))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 5))
				run(fixture.Select(`SELECT id,v FROM jpm_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), want}, {int64(2), want}, {int64(3), want}, {int64(4), want}, {int64(5), want}}))
			})
		})
	}
}
