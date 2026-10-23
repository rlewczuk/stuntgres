package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceJSONBRecordCancellation(t *testing.T) {
	for _, attempt := range []string{"read", "write", "valid"} {
		t.Run(attempt, func(t *testing.T) {
			jsonbRecordCancellationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				started := time.Now()
				phase, phaseQuery := "setup", ""
				defer func() {
					if t.Failed() {
						deadline, _ := p.Ctx.Deadline()
						t.Logf("owner=%s backend=%s phase=%s query=%q elapsed=%s remaining=%s context=%v", t.Name(), b.Name, phase, phaseQuery, time.Since(started), time.Until(deadline), p.Ctx.Err())
					}
				}()
				jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_cancel_row AS (a int[])`, `CREATE TABLE jbr_cancel_input(id int,j jsonb)`, `CREATE TABLE jbr_cancel_saved(id int,a int[])`)
				run := func(s fixture.Step) {
					phaseQuery = s.Query
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				run(fixture.Exec(`INSERT INTO jbr_cancel_input VALUES(1,'{"a":[1]}'),(2,'{"a":[1]}'),(3,'{"a":[1]}'),(4,'{"a":[1]}'),(5,'{"a":[1]}'),(6,'{"a":[1]}'),(7,'{"a":[1]}'),(8,'{"a":[1]}'),(9,'{"a":[1]}')`, 9))
				run(fixture.Exec(`INSERT INTO jbr_cancel_saved VALUES(0,ARRAY[99])`, 1))
				readSQL := `SELECT sum(cardinality(r.a)) AS n FROM jbr_cancel_input d,jsonb_populate_record(NULL::jbr_cancel_row,d.j) r`
				writeSQL := `INSERT INTO jbr_cancel_saved SELECT d.id,r.a FROM jbr_cancel_input d,jsonb_populate_record(NULL::jbr_cancel_row,d.j) r ORDER BY d.id`
				phase, phaseQuery = "prepare/read", readSQL
				read, err := c.PrepareContext(p.Ctx, readSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				phase, phaseQuery = "prepare/write", writeSQL
				write, err := c.PrepareContext(p.Ctx, writeSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer write.Close()
				phase = "precancel/direct-held-query-exec-prepare"
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, held := range []bool{false, true} {
						var rows *sql.Rows
						if held {
							rows, err = read.QueryContext(ctx)
						} else {
							rows, err = c.QueryContext(ctx, readSQL)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("precancel published rows")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
						var result sql.Result
						if held {
							result, err = write.ExecContext(ctx)
						} else {
							result, err = c.ExecContext(ctx, writeSQL)
						}
						if result != nil {
							t.Fatal("precancel published result")
						}
						fixture.LexicalOrdinary(t, err, context.Canceled)
					}
					stmt, err := c.PrepareContext(ctx, readSQL)
					if stmt != nil {
						stmt.Close()
						t.Fatal("precancel published statement")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				// Actual stored JSONB array traversal, not parameter-import cancellation.
				large := `{"a":[` + strings.Repeat("1,", (1<<20)-1) + `1]}`
				if attempt != "valid" {
					writing := attempt == "write"
					phase = "import"
					run(fixture.Prepared(fixture.Exec(`UPDATE jbr_cancel_input SET j=$1 WHERE id>1`, 8, large)))
					query := readSQL
					if writing {
						query = writeSQL
					}
					phase, phaseQuery = attempt+"/admission-cancel-join", query
					fixture.BinaryConversionActiveCancel(t, p, b, c, query, "sqltype.JSONBPopulateRecord", func(ctx context.Context) fixture.RelationOutcome {
						if writing {
							result, err := write.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						rows, err := read.QueryContext(ctx)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					phase = "recovery/sentinel-small-same-held-read"
					run(fixture.Select(`SELECT id,a::text AS a FROM jbr_cancel_saved`, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "{99}"}}))
					run(fixture.Exec(`UPDATE jbr_cancel_input SET j='{"a":[1]}' WHERE id>1`, 8))
					phaseQuery = readSQL
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
				if attempt == "valid" {
					validSQL := `SELECT count(*) AS n FROM jbr_cancel_input WHERE jsonb_populate_record_valid(NULL::jbr_cancel_row,j)`
					phase, phaseQuery = "prepare/valid", validSQL
					valid, err := c.PrepareContext(p.Ctx, validSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer valid.Close()
					phase = "import/valid"
					run(fixture.Prepared(fixture.Exec(`UPDATE jbr_cancel_input SET j=$1 WHERE id>1`, 8, large)))
					phase, phaseQuery = "valid/admission-cancel-join", validSQL
					fixture.BinaryConversionActiveCancel(t, p, b, c, validSQL, "sqltype.JSONBPopulateRecord", func(ctx context.Context) fixture.RelationOutcome {
						rows, err := valid.QueryContext(ctx)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					phase = "recovery/small-same-held-valid"
					run(fixture.Exec(`UPDATE jbr_cancel_input SET j='{"a":[1]}' WHERE id>1`, 8))
					phaseQuery = validSQL
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: valid, SQL: validSQL}, b.Recorder, fixture.Select(validSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
				if attempt == "write" {
					// A later row fails after the first could have produced a write prefix.
					phase = "late-failure/same-held-write"
					run(fixture.Exec(`UPDATE jbr_cancel_input SET j='{"a":["bad"]}' WHERE id=9`, 1))
					d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
					s := fixture.ErrorDiagnostic(writeSQL, d)
					s.ErrorStage = "execute"
					phaseQuery = writeSQL
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, s)
					run(fixture.Select(`SELECT id,a::text AS a FROM jbr_cancel_saved`, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "{99}"}}))
					phase = "late-failure/CTAS-absent"
					run(fixture.ErrorDiagnostic(`CREATE TABLE jbr_cancel_ctas AS SELECT r.a FROM jbr_cancel_input d,jsonb_populate_record(NULL::jbr_cancel_row,d.j) r`, d))
					run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='jbr_cancel_ctas'`, []string{"relname"}, []string{"NAME"}, nil))
					phase = "recovery/same-held-write"
					run(fixture.Exec(`UPDATE jbr_cancel_input SET j='{"a":[1]}' WHERE id=9`, 1))
					phaseQuery = writeSQL
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
					run(fixture.Select(`SELECT count(*) AS n FROM jbr_cancel_saved`, []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
				}
			})
		})
	}
}

// Each independent attempt and backend owns its budget before setup. The
// cancellation, join and same-held recovery remain on that original context.
func jsonbRecordCancellationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		name := "memory"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewFixture(t, postgres, true)
			started := time.Now()
			defer func() {
				if t.Failed() {
					deadline, _ := p.Ctx.Deadline()
					t.Logf("owner=%s phase=setup-or-body elapsed=%s remaining=%s context=%v", t.Name(), time.Since(started), time.Until(deadline), p.Ctx.Err())
				}
			}()
			for _, b := range p.Backends() {
				if b.PG != postgres {
					continue
				}
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				body(t, p, b, c)
			}
		})
	}
}
