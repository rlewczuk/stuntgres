package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableLifecycle(t *testing.T) {
	t.Run("eager invocation", func(t *testing.T) {
		jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, projection := range []string{"r", "v"} {
				sqljsonDiagnostic(t, p, b, c, `SELECT `+projection+` FROM JSON_TABLE($1::jsonb,'$[*]' COLUMNS(r FOR ORDINALITY,v int PATH '$' ERROR ON ERROR)) LIMIT 1`, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), false, `[1,"bad"]`)
			}
			sqljsonBoth(t, p, b, c, jsonTableMetadata(fixture.Select(`SELECT * FROM JSON_TABLE($1::jsonb,'$[*]' COLUMNS(r FOR ORDINALITY,v int PATH '$' ERROR ON ERROR)) LIMIT 0`, []string{"r", "v"}, []string{"INT4", "INT4"}, nil, `[1,"bad"]`), b.PG))
		})
	})
	t.Run("statement atomicity", func(t *testing.T) {
		jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE jt_atomic(id int PRIMARY KEY,v int)`, 0))
			run(fixture.Exec(`INSERT INTO jt_atomic VALUES(0,99),(1,10)`, 2))
			source := `JSON_TABLE($1::jsonb,'$[*]' COLUMNS(id int,v int ERROR ON ERROR)) j`
			for _, q := range []string{
				`INSERT INTO jt_atomic SELECT id,v FROM ` + source,
				`UPDATE jt_atomic t SET v=j.v FROM ` + source + ` WHERE t.id=j.id`,
				`DELETE FROM jt_atomic t USING ` + source + ` WHERE t.id=j.id`,
				`MERGE INTO jt_atomic t USING ` + source + ` ON t.id=j.id WHEN MATCHED THEN UPDATE SET v=j.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(j.id,j.v)`,
			} {
				t.Run(q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), `[{"id":1,"v":7},{"id":2,"v":"bad"}]`)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select(`SELECT id,v FROM jt_atomic ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(99)}, {int64(1), int64(10)}}))
					}
				})
			}
		})
	})
	t.Run("active cancellation and same owners", func(t *testing.T) {
		jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE SEQUENCE jt_progress`, 0))
			run(fixture.Exec(`CREATE TABLE jt_cancel_input(doc jsonb,repetitions int)`, 0))
			run(fixture.Exec(`INSERT INTO jt_cancel_input VALUES('["x"]',1)`, 1))
			run(fixture.Exec(`CREATE TABLE jt_cancel_out(v int)`, 0))
			run(fixture.Exec(`INSERT INTO jt_cancel_out VALUES(99)`, 1))
			source := ` FROM jt_cancel_input d CROSS JOIN LATERAL generate_series(1,d.repetitions) g(n) CROSS JOIN JSON_TABLE(d.doc,'$[*]?(@ like_regex "x+$" && @ == "never")' PASSING nextval('jt_progress') AS unused COLUMNS(o FOR ORDINALITY)) j`
			readSQL := `SELECT o AS v` + source + ` ORDER BY v`
			writeSQL := `INSERT INTO jt_cancel_out SELECT o` + source
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
			large := `["` + strings.Repeat("x", 32<<20) + `",` + strings.Repeat(`"x",`, 1<<16) + `"x"]`
			admitted := func(ctx context.Context) {
				if !b.PG {
					return
				}
				for {
					var called bool
					if err := fixture.Admin.QueryRowContext(ctx, `SELECT is_called FROM public.jt_progress`).Scan(&called); err != nil {
						t.Fatal(err)
					}
					if called {
						return
					}
					if ctx.Err() != nil {
						t.Fatal(ctx.Err())
					}
				}
			}
			for _, writing := range []bool{false, true} {
				run(fixture.Prepared(fixture.Exec(`UPDATE jt_cancel_input SET doc=$1::jsonb,repetitions=100`, 1, large)))
				run(sqljsonQueryOne(`SELECT setval('jt_progress',1,false) AS v`, "INT8", int64(1)))
				q := readSQL
				if writing {
					q = writeSQL
				}
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "sqltype.(*jsonpathExecutor).walk", admitted, func(ctx context.Context) fixture.RelationOutcome {
					if writing {
						result, err := write.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					}
					rows, err := read.QueryContext(ctx)
					if !b.PG || rows == nil {
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					for rows.Next() {
						t.Error("canceled sorted read published a row")
					}
					if err == nil {
						err = rows.Err()
					}
					closed := rows.Close()
					if err == nil {
						err = closed
					}
					return fixture.RelationOutcome{Err: err}
				})
				run(fixture.Select(`SELECT v FROM jt_cancel_out`, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
				run(fixture.Exec(`UPDATE jt_cancel_input SET doc='["never"]',repetitions=1`, 1))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"v"}, []string{"INT4"}, nil))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 0))
			}
		})
	})
}
