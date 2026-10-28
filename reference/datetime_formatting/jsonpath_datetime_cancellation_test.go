package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpdt_active(id int,v jsonb,p jsonpath)`, 0))
				run(fixture.Exec(`CREATE TABLE jpdt_cancel_out(id int,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpdt_active VALUES(1,'["15.08.2023"]','$[*].datetime("DD.MM.YYYY")'),(2,'["15.08.2023"]','$[*].datetime("DD.MM.YYYY")')`, 2))
				run(fixture.Exec(`INSERT INTO jpdt_cancel_out VALUES(0,'99')`, 1))
				gate := `WITH produced AS MATERIALIZED (SELECT jsonb_path_query_array(v,p) AS v FROM jpdt_active WHERE id=1), progressed AS MATERIALIZED (SELECT v,pg_advisory_xact_lock(2::bigint) AS progress FROM produced), gate AS MATERIALIZED (SELECT pg_advisory_xact_lock(CASE WHEN v::text='["2023-08-15"]' THEN 67108864::bigint ELSE 2::bigint END) AS ready FROM progressed) `
				readSQL := gate + `SELECT sum(length(jsonb_path_query_array(v,p)::text)) AS n FROM jpdt_active WHERE (SELECT count(*) FROM gate)=1`
				writeSQL := gate + `INSERT INTO jpdt_cancel_out SELECT id,jsonb_path_query_array(v,p) FROM jpdt_active WHERE (SELECT count(*) FROM gate)=1 ORDER BY id`
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
				var reader, writer fixture.Executor = c, c
				if prepared {
					reader = fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
					writer = fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}
				}
				if !b.PG {
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := error(context.Canceled)
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(1, 0))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						rows, err := reader.QueryContext(ctx, readSQL)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled read returned rows")
						}
						fixture.LexicalOrdinary(t, err, want)
						result, err := writer.ExecContext(ctx, writeSQL)
						if result != nil {
							t.Fatal("canceled write returned result")
						}
						fixture.LexicalOrdinary(t, err, want)
						stmt, err := c.PrepareContext(ctx, jsonpathDatetimeSQL)
						if stmt != nil {
							stmt.Close()
							t.Fatal("canceled Prepare returned statement")
						}
						fixture.LexicalOrdinary(t, err, want)
						cancel()
					}
				}
				large := `[` + strings.Repeat(`"15.08.2023",`, 131071) + `"15.08.2023"]`
				run(fixture.Prepared(fixture.Exec(`UPDATE jpdt_active SET v=$1 WHERE id=2`, 1, large)))
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, `UPDATE jpdt_active SET v='["15.08.2023"]' WHERE id=2`); err != nil {
						t.Error(err)
					}
				}()
				fixture.OutputGateCancel(t, p, b, c, false, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := reader.QueryContext(ctx, readSQL)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec(`UPDATE jpdt_active SET v='["15.08.2023"]' WHERE id=2`, 1))
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(28)}}))
				run(fixture.Prepared(fixture.Exec(`UPDATE jpdt_active SET v=$1 WHERE id=2`, 1, large)))
				fixture.OutputGateCancel(t, p, b, c, true, func(ctx context.Context) fixture.RelationOutcome {
					result, err := writer.ExecContext(ctx, writeSQL)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				run(fixture.Select(`SELECT id,v FROM jpdt_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				run(fixture.Exec(`UPDATE jpdt_active SET v='["15.08.2023"]' WHERE id=2`, 1))
				fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec(writeSQL, 2))
				run(fixture.Select(`SELECT id,v FROM jpdt_cancel_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `["2023-08-15"]`}, {int64(2), `["2023-08-15"]`}}))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-16"]`, `"2023-08-16"`, `$.date()`, `{}`, false))
			})
		})
	}
}

func TestReferenceJSONPathDatetimeAtomicity(t *testing.T) {
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpdt_atomic(id int PRIMARY KEY,doc jsonb,v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpdt_atomic VALUES(0,'"2023-08-15"','99')`, 1))
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		d := jsonpathDatetimeFormatError("date", "bad")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO jpdt_atomic SELECT id,doc,jsonb_path_query_array(doc,'$.date()') FROM (VALUES(1,'"2023-08-16"'::jsonb),(2,'"bad"'::jsonb)) AS s(id,doc) ORDER BY id`, d)
			s.Prepared = prepared
			run(s)
		}
		check := fixture.Select(`SELECT id,doc,v FROM jpdt_atomic ORDER BY id`, []string{"id", "doc", "v"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(0), `"2023-08-15"`, `99`}})
		run(check)
		if !b.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed temporal write changed dump")
			}
		}
		run(fixture.Exec(`INSERT INTO jpdt_atomic VALUES(1,'"bad"','98')`, 1))
		run(fixture.ErrorDiagnostic(`UPDATE jpdt_atomic SET v=jsonb_path_query_array(doc,'$.date()')`, d))
		run(fixture.Select(`SELECT id,v FROM jpdt_atomic ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}, {int64(1), `98`}}))
		run(fixture.Exec(`BEGIN`, 0))
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		run(fixture.Exec(`SAVEPOINT temporal`, 0))
		s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('"bad"','$.date()')`, d)
		s.SelectRows = true
		run(s)
		aborted := fixture.ControlsReferenceDiagnostic("25P02", `current transaction is aborted, commands ignored until end of transaction block`)
		s = fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('"2023-08-15"','$.date()')`, aborted)
		s.SelectRows = true
		run(s)
		run(fixture.Exec(`ROLLBACK TO SAVEPOINT temporal`, 0))
		run(jsonpathNavigateSelect(`SELECT jsonb_path_query_array('"2023-08-15"','$.date()') AS changed`, `["2023-08-15"]`))
		run(fixture.Exec(`COMMIT`, 0))
	})
}
