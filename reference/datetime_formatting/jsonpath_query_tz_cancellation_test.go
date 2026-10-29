package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceJSONPathQueryTZCancellation(t *testing.T) {
	for _, f := range jsonpathQueryTZFunctions {
		t.Run(f.name, func(t *testing.T) {
			jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
				run(fixture.Exec(`CREATE TABLE jpqtz_active(id int,doc jsonb,path jsonpath,repetitions int)`, 0))
				run(fixture.Exec(`INSERT INTO jpqtz_active VALUES(1,'[0]','exists($[*])',1),(2,'[0]','exists($[*])',1),(3,'[0]','exists($[*])',1)`, 3))
				run(fixture.Exec(`CREATE TABLE jpqtz_cancel_out(id int,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpqtz_cancel_out VALUES(0,'99')`, 1))
				run(fixture.Exec(`CREATE SEQUENCE jpqtz_cancel_progress`, 0))
				reset := func() {
					run(fixture.Select(`SELECT setval('jpqtz_cancel_progress',1,false) AS progress`, []string{"progress"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
				// The date-to-timestamptz comparison is demanded before the expensive
				// predicate. A renamed timezone-independent regex workload is insufficient.
				path := `strict $[*]?(@.t.timestamp_tz() > $limit.datetime() && @.x like_regex "x+$" && @.x == "never")`
				if f.name == "jsonb_path_match_tz" {
					path = `strict exists($[*]?(@.t.timestamp_tz() > $limit.datetime() && @.x like_regex "x+$" && @.x == "never"))`
				}
				large := `[{"t":"2023-08-15 00:00:00+00","x":"` + strings.Repeat("x", 32<<20) + `"}]`
				source := ` FROM jpqtz_active CROSS JOIN LATERAL generate_series(1,repetitions) AS repeat(n)`
				call := f.name + `(doc,path,jsonb_build_object('progress',nextval('jpqtz_cancel_progress'),'limit','2023-08-14'))`
				readSQL := `SELECT ` + call + ` AS changed` + source + ` ORDER BY changed,id`
				writeSQL := `INSERT INTO jpqtz_cancel_out SELECT id,to_jsonb(` + call + `)` + source + ` ORDER BY id`
				if f.set {
					writeSQL = `INSERT INTO jpqtz_cancel_out SELECT id,` + call + source + ` ORDER BY id`
				}
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
							ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(0, 0))
							want = context.DeadlineExceeded
						}
						if !expired {
							cancel()
						}
						rows, err := read.QueryContext(ctx)
						if rows != nil {
							rows.Close()
							t.Fatal("interrupted read published rows")
						}
						fixture.LexicalOrdinary(t, err, want)
						result, err := write.ExecContext(ctx)
						if result != nil {
							t.Fatal("interrupted write published result")
						}
						fixture.LexicalOrdinary(t, err, want)
						cancel()
					}
				}
				admitted := func(minimum int64) func(context.Context) {
					return func(ctx context.Context) {
						if !b.PG {
							return
						}
						for {
							var value int64
							var called bool
							if err := fixture.Admin.QueryRowContext(ctx, `SELECT last_value,is_called FROM public.jpqtz_cancel_progress`).Scan(&value, &called); err != nil {
								t.Fatal(err)
							}
							if called && value >= minimum {
								return
							}
							if ctx.Err() != nil {
								t.Fatal(ctx.Err())
							}
						}
					}
				}
				provider := "sqltype.JSONBPathQueryPlan.Evaluate"
				switch f.name {
				case "jsonb_path_exists_tz":
					provider = "sqltype.JSONBPathExistsPlan.Evaluate"
				case "jsonb_path_match_tz":
					provider = "sqltype.JSONBPathMatchPlan.Evaluate"
				case "jsonb_path_query_first_tz":
					provider = "sqltype.JSONBPathQueryFirstPlan.Evaluate"
				case "jsonb_path_query_array_tz":
					provider = "sqltype.JSONBPathQueryArrayPlan.Evaluate"
				}
				run(fixture.Prepared(fixture.Exec(`UPDATE jpqtz_active SET doc=$1,path=$2,repetitions=100`, 3, large, path)))
				reset()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, readSQL, provider, admitted(1), func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					if !b.PG || rows == nil {
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					published := false
					for rows.Next() {
						published = true
					}
					if published {
						t.Error("canceled read published row")
					}
					terminal := rows.Err()
					closeErr := rows.Close()
					if terminal == nil {
						terminal = err
					}
					if terminal == nil {
						terminal = closeErr
					}
					return fixture.RelationOutcome{Err: terminal}
				})
				run(fixture.Exec(`UPDATE jpqtz_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				if f.name == "jsonb_path_query_array_tz" {
					want = `[true]`
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, jsonpathQueryResult(readSQL, f.typ, jsonpathQueryRows(want, want, want)))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpqtz_active SET doc=$1,path=$2,repetitions=100 WHERE id>1`, 2, large, path)))
				reset()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, writeSQL, provider, admitted(2), func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpqtz_cancel_out`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpqtz_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 3))
				v := `true`
				if f.name == "jsonb_path_query_array_tz" {
					v = `[true]`
				}
				run(fixture.Select(`SELECT id,v FROM jpqtz_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), v}, {int64(2), v}, {int64(3), v}}))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, jsonpathQueryOne(`SELECT jsonb_path_query_first_tz('"2023-08-15"','$.timestamp_tz()') AS changed`, "JSONB", `"2023-08-15T00:00:00+00:00"`))
			})
		})
	}
}
