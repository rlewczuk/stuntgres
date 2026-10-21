package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryLifecycle(t *testing.T) {
	t.Run("controls", func(t *testing.T) {
		sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			check := sqljsonQueryStored(t, p, b, c)
			future := []fixture.Step{
				fixture.Exec(`UPDATE sqjq_stored SET doc='{"a":[3],"s":"7"}' WHERE id=1`, 1),
				fixture.Select(`SELECT found,items,value FROM sqjq_view`, []string{"found", "items", "value"}, []string{"BOOL", "JSONB", "INT4"}, [][]any{{true, `[3]`, int64(7)}}),
			}
			dump := jsonpathNavigateReplay(t, p, b, c, check, future...)
			if !b.PG {
				unchanged := func() {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump, after.Bytes()) {
						t.Fatal("failed Restore changed persistent state")
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
				}
				for slot := range 5 {
					args := []string{`'{}'`, `'$.x'`, `1`, `7`, `8`}
					args[slot] = "$1"
					script := `/*é🙂*/ CREATE TABLE sqjq_bad(v int); INSERT INTO sqjq_bad VALUES(JSON_VALUE(` + args[0] + `,` + args[1] + ` PASSING ` + args[2] + ` AS x RETURNING int DEFAULT ` + args[3] + ` ON EMPTY DEFAULT ` + args[4] + ` ON ERROR));`
					d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
					d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
					unchanged()
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
				unchanged()
			}
			for _, s := range future {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		})
	})
	t.Run("later row conversion atomicity", func(t *testing.T) {
		sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE sqjq_input(id int,doc jsonb)`, 0))
			run(fixture.Exec(`INSERT INTO sqjq_input VALUES(1,'"7"'),(2,'"bad"')`, 2))
			run(fixture.Exec(`CREATE TABLE sqjq_out(v int)`, 0))
			run(fixture.Exec(`INSERT INTO sqjq_out VALUES(99)`, 1))
			for _, fn := range []string{"JSON_VALUE", "JSON_QUERY"} {
				clause := ""
				if fn == "JSON_QUERY" {
					clause = " OMIT QUOTES"
				}
				for _, prefix := range []string{`INSERT INTO sqjq_out SELECT `, `CREATE TABLE sqjq_failed AS SELECT `} {
					t.Run(fn+prefix, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						q := prefix + fn + `(doc,'$' RETURNING int` + clause + ` ERROR ON ERROR) AS v FROM sqjq_input ORDER BY id`
						d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared, s.ErrorStage = prepared, "execute"
							run(s)
							run(sqljsonQueryOne(`SELECT v FROM sqjq_out`, "INT4", int64(99)))
							run(sqljsonQueryOne(`SELECT count(*) AS v FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='public' AND c.relname='sqjq_failed'`, "INT8", int64(0)))
						}
					})
				}
			}
		})
	})
	for _, fn := range []string{"JSON_EXISTS", "JSON_QUERY", "JSON_VALUE"} {
		t.Run(fn+" reached cancellation", func(t *testing.T) {
			sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE sqjq_active(id int,doc jsonb,path jsonpath,repetitions int)`, 0))
				run(fixture.Exec(`INSERT INTO sqjq_active VALUES(1,'7','$',1),(2,'7','$',1),(3,'7','$',1)`, 3))
				run(fixture.Exec(`CREATE SEQUENCE sqjq_progress`, 0))
				run(fixture.Exec(`CREATE TABLE sqjq_cancel_out(id int,v text)`, 0))
				run(fixture.Exec(`INSERT INTO sqjq_cancel_out VALUES(0,'sentinel')`, 1))
				call := fn + `(doc,path PASSING nextval('sqjq_progress') AS unused)::text`
				source := ` FROM sqjq_active CROSS JOIN LATERAL generate_series(1,repetitions) AS repeat(n)`
				readSQL := `SELECT ` + call + ` AS v` + source + ` ORDER BY v,id`
				writeSQL := `INSERT INTO sqjq_cancel_out SELECT id,` + call + source + ` ORDER BY id`
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
						t.Fatal("precanceled query published rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					result, err := write.ExecContext(ctx)
					if result != nil {
						t.Fatal("precanceled write published result")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				large := `["` + strings.Repeat("x", 32<<20) + `",` + strings.Repeat("0,", 1<<16) + `0]`
				path := `strict $[*] ? (@ like_regex "x+$" && @ == "never")`
				admitted := func(minimum int64) func(context.Context) {
					return func(ctx context.Context) {
						if !b.PG {
							return
						}
						for {
							var value int64
							var called bool
							if err := fixture.Admin.QueryRowContext(ctx, `SELECT last_value,is_called FROM public.sqjq_progress`).Scan(&value, &called); err != nil {
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
				resetProgress := func() { run(sqljsonQueryOne(`SELECT setval('sqjq_progress',1,false) AS v`, "INT8", int64(1))) }
				run(fixture.Prepared(fixture.Exec(`UPDATE sqjq_active SET doc=$1,path=$2,repetitions=100`, 3, large, path)))
				resetProgress()
				// Existing shared walker frame, not a nonexistent query-plan symbol.
				provider := "sqltype.(*jsonpathExecutor).walk"
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, readSQL, provider, admitted(1), func(ctx context.Context) fixture.RelationOutcome {
					rows, err := read.QueryContext(ctx)
					if !b.PG || rows == nil {
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					// pq can expose RowDescription before execution; no successful
					// row is permitted here because the computed sort demands all work.
					for rows.Next() {
						t.Error("active canceled sorted read published row")
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
				run(fixture.Exec(`UPDATE sqjq_active SET doc='7',path='$',repetitions=1`, 3))
				want := "7"
				if fn == "JSON_EXISTS" {
					want = "true"
				}
				held := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(readSQL, []string{"v"}, []string{"TEXT"}, [][]any{{want}, {want}, {want}}))
				run(fixture.Prepared(fixture.Exec(`UPDATE sqjq_active SET doc=$1,path=$2,repetitions=100 WHERE id>1`, 2, large, path)))
				resetProgress()
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, writeSQL, provider, admitted(2), func(ctx context.Context) fixture.RelationOutcome {
					result, err := write.ExecContext(ctx)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM sqjq_cancel_out`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
				run(fixture.Exec(`UPDATE sqjq_active SET doc='7',path='$',repetitions=1`, 3))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 3))
				run(fixture.Select(`SELECT id,v FROM sqjq_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}, {int64(1), want}, {int64(2), want}, {int64(3), want}}))
			})
		})
	}
}
