package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceJSONPathArithmeticCancellation(t *testing.T) {
	for _, workload := range []struct{ path, control, want string }{
		{`-$[*]`, `[-2,-3]`, `[2, 3]`},
		{`$[*]?(@ * @ > 0)`, `[2,3]`, `[2, 3]`},
	} {
		t.Run(workload.path, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpa_active(id int,v jsonb,p jsonpath)`, 0))
				run(fixture.Exec(`CREATE TABLE jpa_cancel_out(id int,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpa_active VALUES(1,'[2,3]','$[*]'),(2,'[2,3]','$[*]'),(3,'[2,3]','$[*]'),(4,'[2,3]','$[*]'),(5,'[2,3]','$[*]')`, 5))
				run(fixture.Exec(`INSERT INTO jpa_cancel_out VALUES(0,'99')`, 1))
				// Typed exact numeric work, below the established 32MiB payload bound.
				element := strings.Repeat("9", 4092)
				large := `[` + strings.Repeat(element+",", 8191) + element + `]`
				gate := `WITH produced AS MATERIALIZED (
					SELECT jsonb_path_query_array(v,p,'{}',true) AS v FROM jpa_active WHERE id=1
				), progressed AS MATERIALIZED (
					SELECT v,pg_advisory_xact_lock(2::bigint) AS progress FROM produced
				), gate AS MATERIALIZED (
					SELECT pg_advisory_xact_lock(CASE WHEN v::text='` + workload.want + `' THEN 67108864::bigint ELSE 2::bigint END) AS ready FROM progressed
				) `
				readSQL := gate + `SELECT sum(length(jsonb_path_query_array(v,p,'{}',true)::text)) AS n FROM jpa_active WHERE (SELECT count(*) FROM gate)=1`
				writeSQL := gate + `INSERT INTO jpa_cancel_out SELECT id,jsonb_path_query_array(v,p,'{}',true) FROM jpa_active WHERE (SELECT count(*) FROM gate)=1 ORDER BY id`
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
					for _, route := range []struct{ read, write fixture.Executor }{
						{c, c},
						{fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}},
					} {
						rows, err := route.read.QueryContext(ctx, readSQL)
						if rows != nil {
							rows.Close()
							t.Fatal("precanceled read published rows")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
						result, err := route.write.ExecContext(ctx, writeSQL)
						if result != nil {
							t.Fatal("precanceled write published result")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
					}
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, `UPDATE jpa_active SET v='[2,3]',p='$[*]'`); err != nil {
						t.Error(err)
					}
				}()
				load := func() {
					run(fixture.Prepared(fixture.Exec(`UPDATE jpa_active SET v=$1,p=$2 WHERE id>1`, 4, large, workload.path)))
					run(fixture.Prepared(fixture.Exec(`UPDATE jpa_active SET v=$1,p=$2 WHERE id=1`, 1, workload.control, workload.path)))
				}
				load()
				fixture.OutputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec(`UPDATE jpa_active SET v='[2,3]',p='$[*]'`, 5))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(30)}}))
				load()
				fixture.OutputGateCancel(t, p, b, c, true, func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpa_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpa_active SET v='[2,3]',p='$[*]'`, 5))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 5))
				run(fixture.Select(`SELECT id,v FROM jpa_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `[2, 3]`}, {int64(2), `[2, 3]`}, {int64(3), `[2, 3]`}, {int64(4), `[2, 3]`}, {int64(5), `[2, 3]`}}))
			})
		})
	}
}
