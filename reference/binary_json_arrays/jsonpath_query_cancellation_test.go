package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryCancellation(t *testing.T) {
	for _, tc := range []struct{ name, provider, path string }{
		{"jsonb_path_exists", "sqltype.JSONBPathExistsPlan.Evaluate", `strict $[*]?(@ like_regex "x+$" && @ == "never")`},
		{"jsonb_path_match", "sqltype.JSONBPathMatchPlan.Evaluate", `strict exists($[*]?(@ like_regex "x+$" && @ == "never"))`},
		{"jsonb_path_query_first", "sqltype.JSONBPathQueryFirstPlan.Evaluate", `$[*]?(@ like_regex "x+$" && @ == "never")`},
		{"jsonb_path_query", "sqltype.JSONBPathQueryPlan.Evaluate", `$[*]?(@ like_regex "x+$" && @ == "never")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each consumer owns its fixture deadline, records and held recovery.
			jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpq_active(id int,doc jsonb,path jsonpath,repetitions int)`, 0))
				run(fixture.Exec(`INSERT INTO jpq_active VALUES(1,'[0]','exists($[*])',1),(2,'[0]','exists($[*])',1),(3,'[0]','exists($[*])',1)`, 3))
				run(fixture.Exec(`CREATE TABLE jpq_cancel_out(id int,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpq_cancel_out VALUES(0,'99')`, 1))
				run(fixture.Exec(`CREATE SEQUENCE jpq_cancel_progress`, 0))
				resetProgress := func() {
					run(fixture.Select(`SELECT setval('jpq_cancel_progress',1,false) AS progress`, []string{"progress"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
				large := `["` + strings.Repeat("x", 32<<20) + `",` + strings.Repeat("0,", 1<<16) + `0]`
				// Evaluate the expensive predicate but never transfer its large input.
				path := tc.path
				// Sort by the computed result: source-id ordering alone can precede
				// PG ProjectSet and publish an earlier invocation before cancellation.
				source := ` FROM jpq_active CROSS JOIN LATERAL generate_series(1,repetitions) AS repeat(n)`
				call := tc.name + `(doc,path,jsonb_build_object('progress',nextval('jpq_cancel_progress')))`
				readSQL := `SELECT ` + call + ` AS changed` + source + ` ORDER BY changed,id`
				writeSQL := `INSERT INTO jpq_cancel_out SELECT id,to_jsonb(` + call + `)` + source + ` ORDER BY id`
				// SRFs cannot be nested in to_jsonb; the set already returns JSONB.
				if tc.name == "jsonb_path_query" {
					writeSQL = `INSERT INTO jpq_cancel_out SELECT id,` + call + source + ` ORDER BY id`
				}
				admitted := func(minimum int64) func(context.Context) {
					return func(ctx context.Context) {
						if !b.PG {
							return
						}
						for {
							var value int64
							var called bool
							if err := fixture.Admin.QueryRowContext(ctx, `SELECT last_value,is_called FROM public.jpq_cancel_progress`).Scan(&value, &called); err != nil {
								t.Fatal("executor progress observation", err)
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
					rows, err := read.QueryContext(ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled read published rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					result, err := write.ExecContext(ctx)
					if result != nil {
						t.Fatal("precanceled write published result")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				// Every read invocation must do real evaluator work before any result.
				// Writes below retain a cheap prior invocation to prove rollback.
				run(fixture.Prepared(fixture.Exec(`UPDATE jpq_active SET doc=$1,path=$2,repetitions=100`, 3, large, path)))
				resetProgress()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, readSQL, tc.provider, admitted(1), func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					if !b.PG || rows == nil {
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					// pq may publish RowDescription before evaluation finishes.
					// Drain to EOF to observe the actual cancellation diagnostic.
					published := false
					for rows.Next() {
						published = true
					}
					if published {
						t.Error("active canceled query published a successful row")
					}
					observedErr := rows.Err()
					closeErr := rows.Close()
					if observedErr == nil {
						observedErr = err
					}
					if observedErr == nil {
						observedErr = closeErr
					}
					return fixture.RelationOutcome{Err: observedErr}
				})
				run(fixture.Exec(`UPDATE jpq_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				want := any(true)
				typ := "BOOL"
				if tc.name == "jsonb_path_query" || tc.name == "jsonb_path_query_first" {
					want = `true`
					typ = "JSONB"
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, jsonpathQueryResult(readSQL, typ, jsonpathQueryRows(want, want, want)))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpq_active SET doc=$1,path=$2,repetitions=100 WHERE id>1`, 2, large, path)))
				resetProgress()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, writeSQL, tc.provider, admitted(2), func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpq_cancel_out`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpq_active SET doc='[0]',path='exists($[*])',repetitions=1`, 3))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 3))
				run(fixture.Select(`SELECT id,v FROM jpq_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `true`}, {int64(2), `true`}, {int64(3), `true`}}))
				run(fixture.Exec(`DELETE FROM jpq_cancel_out WHERE id>0`, 3))
			})
		})
	}
}
