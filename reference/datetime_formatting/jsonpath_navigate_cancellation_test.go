package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceJSONPathNavigateCancellation(t *testing.T) {
	for _, path := range []string{`$[*]`, `strict $.**`, `$[*]?(@ == @)`, `$[*]?(@ like_regex "x+$")`, `$`} {
		t.Run(path, func(t *testing.T) {
			// Paths are independent workloads, not a cross-path statement-identity
			// contract. Each owns an ordinary bounded fixture; both cancellations
			// and their same-held-statement recoveries stay on its physical owner.
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpn_active(id int,v jsonb,p jsonpath)`, 0))
				run(fixture.Exec(`CREATE TABLE jpn_cancel_out(id int,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpn_active VALUES(1,'[0]','$'),(2,'[0]','$'),(3,'[0]','$'),(4,'[0]','$'),(5,'[0]','$')`, 5))
				run(fixture.Exec(`INSERT INTO jpn_cancel_out VALUES(0,'99')`, 1))
				// Keep the encoded payload below 32MiB with 8192 bounded strings.
				// Admission depends on completed JSONPath output, not a transient
				// private frame or an inflated input.
				element := `"` + strings.Repeat("x", 4092) + `"`
				large := `[` + strings.Repeat(element+",", 8191) + element + `]`
				controlOutput := `["x"]`
				if path == `strict $.**` {
					controlOutput = `[["x"], "x"]`
				} else if path == `$` {
					controlOutput = `[["x"]]`
				}
				// Complete the selected path on a controlled row before admitting
				// the remaining operands; large regex completion is not a timer gate.
				gateSQL := `WITH produced AS MATERIALIZED (
					SELECT jsonb_path_query_array(v,p) AS v FROM jpn_active WHERE id=1
				), progressed AS MATERIALIZED (
					SELECT v,pg_advisory_xact_lock(2::bigint) AS progress FROM produced
				), gate AS MATERIALIZED (
					SELECT pg_advisory_xact_lock(CASE WHEN v::text='` + controlOutput + `' THEN 67108864::bigint ELSE 2::bigint END) AS ready FROM progressed
				) `
				readSQL := gateSQL + `SELECT sum(length(jsonb_path_query_array(v,p)::text)) AS n FROM jpn_active WHERE (SELECT count(*) FROM gate)=1`
				writeSQL := gateSQL + `INSERT INTO jpn_cancel_out SELECT id,jsonb_path_query_array(v,p) FROM jpn_active WHERE (SELECT count(*) FROM gate)=1 ORDER BY id`
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
					for _, query := range []string{
						"DELETE FROM jpn_cancel_out WHERE id>0",
						`UPDATE jpn_active SET v='[0]',p='$' WHERE id>=1`,
					} {
						if _, err := c.ExecContext(ctx, query); err != nil {
							t.Error("restore active cancellation child:", query, err)
						}
					}
				}()
				// Import typed data first; the public gate consumes its real output.
				run(fixture.Prepared(fixture.Exec(`UPDATE jpn_active SET v=$1,p=$2 WHERE id>1`, 4, large, path)))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpn_active SET v='["x"]',p=$1 WHERE id=1`, 1, path)))
				fixture.OutputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec(`UPDATE jpn_active SET v='[0]',p='$' WHERE id>=1`, 5))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(25)}}))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpn_active SET v=$1,p=$2 WHERE id>1`, 4, large, path)))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpn_active SET v='["x"]',p=$1 WHERE id=1`, 1, path)))
				fixture.OutputGateCancel(t, p, b, c, true, func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpn_cancel_out`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpn_active SET v='[0]',p='$' WHERE id>=1`, 5))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 5))
				run(fixture.Select(`SELECT id,v FROM jpn_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `[[0]]`}, {int64(2), `[[0]]`}, {int64(3), `[[0]]`}, {int64(4), `[[0]]`}, {int64(5), `[[0]]`}}))
				run(fixture.Exec(`DELETE FROM jpn_cancel_out WHERE id>0`, 5))
			})
		})
	}
}
