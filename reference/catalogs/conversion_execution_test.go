package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionExecutionAndRetained(t *testing.T) {
	conversionScenario(t, []string{"conv_exec_hidden"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE SCHEMA conv_exec_hidden`, conversionCreate("public.conv_exec", true))
		conversionHideSeed(t, p, b, "conv_exec_hidden")
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`SET search_path=public,pg_catalog`, 0))
		q := `SELECT convert_to($1::text,'LATIN1') AS v`
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, text := range []string{"é", "€", "é"} {
			s := fixture.Select(q, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}, text)
			if text == "€" {
				s = fixture.ErrorDiagnostic(q, conversionEuro(), text)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		values := fixture.Select(`SELECT convert(decode('c3a9','hex'),'UTF8','LATIN1') AS bytes,convert_from(decode('e9','hex'),'LATIN1') AS text,convert_to('€','WIN1252') AS euro,convert_to('','LATIN1') AS empty,convert_from(NULL::bytea,'LATIN1') AS absent`, []string{"bytes", "text", "euro", "empty", "absent"}, []string{"BYTEA", "TEXT", "BYTEA", "BYTEA", "TEXT"}, [][]any{{"\xe9", "é", "\x80", "", nil}})
		for _, prepared := range []bool{false, true} {
			values.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, values)
		}
		bad := fixture.ErrorDiagnostic(`SELECT convert(decode('ff','hex'),'UTF8','LATIN1') AS v`, conversionDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`))
		bad.SelectRows, bad.ErrorStage = true, "execute"
		for _, prepared := range []bool{false, true} {
			bad.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, bad)
		}
		conversionExec(t, p, b, `CREATE TABLE conv_exec_input(id INT,v TEXT)`, `CREATE TABLE conv_exec_output(id INT,v BYTEA)`, `CREATE TABLE conv_exec_checked(v TEXT CHECK(convert_to(v,'LATIN1') IS NOT NULL))`, `CREATE VIEW conv_exec_view AS SELECT convert_to('é','LATIN1') AS v`)
		conversionStep(t, p, b, fixture.Exec(`INSERT INTO conv_exec_input VALUES(1,'é'),(2,'€')`, 2))
		conversionStep(t, p, b, fixture.Exec(`INSERT INTO conv_exec_output VALUES(99,decode('aa','hex'))`, 1))
		for _, query := range []string{`INSERT INTO conv_exec_output SELECT id,convert_to(v,'LATIN1') FROM conv_exec_input ORDER BY id`, `INSERT INTO conv_exec_checked VALUES('é'),('€')`} {
			conversionError(t, p, b, query, conversionEuro(), false)
		}
		conversionStep(t, p, b, fixture.Select(`SELECT id,v FROM conv_exec_output ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(99), "\xaa"}}))
		conversionStep(t, p, b, fixture.Select(`SELECT count(*) FROM conv_exec_checked`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		rows, err := conn.QueryContext(p.Ctx, q, "é")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		// DDL uses another physical owner while the old cursor retains bytes/types.
		conversionExec(t, p, b, `ALTER CONVERSION conv_exec RENAME TO conv_exec_renamed`)
		fixture.UpdateCoreRetained(t, b.PG, rows, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}, false)
		conversionStep(t, p, b, fixture.Select(`SELECT v FROM conv_exec_view`, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}))
		conversionExec(t, p, b, `DROP CONVERSION conv_exec_renamed`)
		missing := fixture.ErrorDiagnostic(q, conversionMissingDefault(), "é")
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, missing)
		conversionError(t, p, b, `SELECT v FROM conv_exec_view`, conversionMissingDefault(), false)
		conversionExec(t, p, b, conversionCreate("public.conv_exec_recovery", true))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}, "é"))
	})
}
