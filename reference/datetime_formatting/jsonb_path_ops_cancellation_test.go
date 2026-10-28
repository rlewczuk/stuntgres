package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsCancellation(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpo_active(id int,doc jsonb,path jsonpath,repetitions int)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_active VALUES(1,'[0]','exists($[*])',1),(2,'[0]','exists($[*])',1),(3,'[0]','exists($[*])',1)`, 3))
		run(fixture.Exec(`CREATE TABLE jpo_cancel_out(id int,v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_cancel_out VALUES(0,'99')`, 1))
		run(fixture.Exec(`CREATE SEQUENCE jpo_cancel_progress`, 0))
		resetProgress := func() {
			run(fixture.Select(`SELECT setval('jpo_cancel_progress',1,false) AS progress`, []string{"progress"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		large := `["` + strings.Repeat("x", 32<<20) + `",` + strings.Repeat("0,", 1<<16) + `0]`
		for _, tc := range []struct{ op, provider, path string }{
			{"@?", "sqltype.JSONBPathExistsPlan.Evaluate", `strict $[*]?(@ like_regex "x+$" && @ == "never")`},
			{"@@", "sqltype.JSONBPathMatchPlan.Evaluate", `strict exists($[*]?(@ like_regex "x+$" && @ == "never"))`},
		} {
			t.Run(tc.op, func(t *testing.T) {
				source := ` FROM jpo_active CROSS JOIN LATERAL generate_series(1,repetitions) AS repeat(n)`
				call := `(CASE WHEN nextval('jpo_cancel_progress')>0 THEN doc ELSE NULL::jsonb END) ` + tc.op + ` path`
				readSQL := `SELECT ` + call + ` AS changed` + source + ` ORDER BY changed,id`
				writeSQL := `INSERT INTO jpo_cancel_out SELECT id,to_jsonb(` + call + `)` + source + ` ORDER BY id`
				admitted := func(minimum int64) func(context.Context) {
					return func(ctx context.Context) {
						if !b.PG {
							return
						}
						for {
							var value int64
							var called bool
							if err := fixture.Admin.QueryRowContext(ctx, `SELECT last_value,is_called FROM public.jpo_cancel_progress`).Scan(&value, &called); err != nil {
								t.Fatal("executor progress", err)
							}
							if called && value >= minimum {
								return
							}
							if ctx.Err() != nil {
								t.Fatal("executor progress deadline", ctx.Err())
							}
						}
					}
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
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, held := range []bool{false, true} {
						var rows *sql.Rows
						var result sql.Result
						var err error
						if held {
							rows, err = read.QueryContext(ctx)
						} else {
							rows, err = c.QueryContext(ctx, readSQL)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("precanceled read published rows")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
						if held {
							result, err = write.ExecContext(ctx)
						} else {
							result, err = c.ExecContext(ctx, writeSQL)
						}
						if result != nil {
							t.Fatal("precanceled write published result")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
					}
				}
				run(fixture.Prepared(fixture.Exec(`UPDATE jpo_active SET doc=$1,path=$2,repetitions=100`, 3, large, tc.path)))
				resetProgress()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, readSQL, tc.provider, admitted(1), func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					if !b.PG || rows == nil {
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					published := false
					for rows.Next() {
						published = true
					}
					if published {
						t.Error("sorted canceled read published a row")
					}
					observed := rows.Err()
					closed := rows.Close()
					if observed == nil {
						observed = err
					}
					if observed == nil {
						observed = closed
					}
					return fixture.RelationOutcome{Err: observed}
				})
				run(fixture.Exec(`UPDATE jpo_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, jsonpathQueryResult(readSQL, "BOOL", jsonpathQueryRows(true, true, true)))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpo_active SET doc=$1,path=$2,repetitions=100 WHERE id>1`, 2, large, tc.path)))
				resetProgress()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, writeSQL, tc.provider, admitted(2), func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpo_cancel_out`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpo_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 3))
				run(fixture.Select(`SELECT id,v FROM jpo_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `true`}, {int64(2), `true`}, {int64(3), `true`}}))
				run(fixture.Exec(`DELETE FROM jpo_cancel_out WHERE id>0`, 3))
			})
		}
	})
}
