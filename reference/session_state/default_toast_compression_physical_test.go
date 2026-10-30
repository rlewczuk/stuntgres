package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func toastInsertPair(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := toastRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE toast_default_case(id integer,payload text)", 0))
	run(toastPolicy("toast_default_case", ""))
	for i, value := range []string{"pglz", "lz4"} {
		run(fixture.Exec("SET default_toast_compression="+value, 0))
		s := fixture.Exec("INSERT INTO toast_default_case(id,payload) VALUES($1::integer,$2::text)", 1, i+1, toastPayload())
		s.Prepared = i == 1
		run(s)
		run(toastPolicy("toast_default_case", ""))
	}
	run(toastRows("toast_default_case", 2))
}

func toastPairDemand(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := toastRun(t, p, b, c)
	q := "SELECT pg_catalog.pg_column_compression(payload) AS compression FROM toast_default_case WHERE id=$1"
	statement, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("prepare typed physical inquiry", err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
	for i, value := range []string{"pglz", "lz4"} {
		run(fixture.Exec("SET default_toast_compression="+value, 0))
		for id, measured := range []string{"pglz", "lz4"} {
			s := toastDemand(b.PG, q, measured, id+1)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(toastText(b.PG, "SHOW default_toast_compression", toastSetting, value))
			run(toastRows("toast_default_case", 2))
			run(toastPolicy("toast_default_case", ""))
		}
		if i == 1 {
			run(fixture.Exec("SET default_toast_compression=pglz", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, toastDemand(b.PG, q, "lz4", 2))
		}
	}
}

func toastInquiryBasics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := toastRun(t, p, b, c)
	for _, s := range []fixture.Step{
		toastInquiryText("SELECT pg_column_compression(NULL::text) AS compression", nil),
		toastInquiryText("SELECT pg_catalog.pg_column_compression($1::text) AS compression", nil),
		fixture.Select("SELECT pg_column_compression(payload) AS compression FROM toast_default_case LIMIT 0", []string{"compression"}, []string{"TEXT"}, nil),
	} {
		if s.Query == "SELECT pg_catalog.pg_column_compression($1::text) AS compression" {
			s.Args = []any{nil}
		}
		run(s)
		run(fixture.Prepared(s))
		toastHeld(t, p, b, c, s)
	}
	for _, q := range []string{"SELECT pg_column_compression(7) AS compression", "SELECT pg_catalog.pg_column_compression('short'::text) AS compression"} {
		s := toastDemand(b.PG, q, nil)
		run(s)
		run(fixture.Prepared(s))
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT pg_column_compression(missing_payload) FROM toast_default_case", fixture.Diagnostic{Code: "42703", Message: `column "missing_payload" does not exist`, Position: fixture.Position(30)})
		s.SelectRows, s.Prepared = true, prepared
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
		for _, call := range []struct{ sql, signature string }{
			{"pg_column_compression()", "pg_column_compression()"},
			{"pg_column_compression(NULL,NULL)", "pg_column_compression(unknown, unknown)"},
		} {
			s := fixture.ImplicitError("SELECT "+call.sql, fixture.Diagnostic{Code: "42883", Message: "function "+call.signature+" does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(8)})
			s.SelectRows, s.Prepared = true, prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
	}
	run(fixture.Select("SELECT oid::integer,proname,proargtypes::text,prorettype::integer,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=2121", []string{"oid", "proname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel"}, []string{"INT4", "NAME", "TEXT", "INT4", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(2121), "pg_column_compression", "2276", int64(25), true, "s", "s"}}))
}

func TestReferenceDefaultToastCompressionPhysicalBoundary(t *testing.T) {
	toastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := toastRun(t, p, b, c)
		toastInsertPair(t, p, b, c)
		toastPairDemand(t, p, b, c)
		toastInquiryBasics(t, p, b, c)
		run(fixture.Exec("CREATE TABLE toast_explicit_case(id integer,payload text)", 0))
		run(fixture.Prepared(fixture.Exec("ALTER TABLE toast_explicit_case ALTER COLUMN payload SET COMPRESSION pglz", 0)))
		run(fixture.Exec("SET default_toast_compression=lz4", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO toast_explicit_case(id,payload) VALUES($1::integer,$2::text)", 1, 1, toastPayload())))
		run(toastPolicy("toast_explicit_case", "p"))
		run(toastRows("toast_explicit_case", 1))
		for _, prepared := range []bool{false, true} {
			s := toastDemand(b.PG, "SELECT pg_column_compression(payload) AS compression FROM toast_explicit_case", "pglz")
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("CREATE VIEW toast_compression_view AS SELECT pg_column_compression(payload) AS compression FROM toast_default_case", 0))
		empty := fixture.Select("SELECT compression FROM toast_compression_view LIMIT 0", []string{"compression"}, []string{"TEXT"}, nil)
		run(empty)
		run(fixture.Prepared(empty))
		toastHeld(t, p, b, c, empty)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("toast-restored")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		target, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		destination := b
		destination.DB, destination.Recorder = recorded, recorder
		replay := toastRun(t, p, destination, target)
		replay(empty)
		replay(fixture.Prepared(empty))
		replay(toastRows("toast_default_case", 2))
		replay(toastPolicy("toast_default_case", ""))
		replay(toastDemand(false, "SELECT compression FROM toast_compression_view", nil))
		replay(toastRows("toast_default_case", 2))
	})
}

func TestReferenceDefaultToastCompressionFeatureSmoke(t *testing.T) {
	toastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := toastRun(t, p, b, c)
		toastInsertPair(t, p, b, c)
		for _, s := range toastState(b.PG, "lz4", "session") {
			run(s)
		}
		q := "SELECT pg_column_compression(payload) AS compression FROM toast_default_case WHERE id=$1"
		for i, method := range []string{"pglz", "lz4"} {
			toastHeld(t, p, b, c, toastDemand(b.PG, q, method, i+1))
		}
		run(toastInquiryText("SELECT pg_column_compression(NULL::text) AS compression", nil))
		run(fixture.Select("SELECT pg_column_compression(payload) AS compression FROM toast_default_case LIMIT 0", []string{"compression"}, []string{"TEXT"}, nil))
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		txrun := toastRun(t, p, b, tx)
		for _, q := range []string{"SAVEPOINT toast_smoke", "SET default_toast_compression=pglz", "ROLLBACK TO toast_smoke", "RELEASE toast_smoke"} {
			txrun(fixture.Exec(q, 0))
		}
		for _, s := range toastState(b.PG, "lz4", "session") {
			txrun(s)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(toastRows("toast_default_case", 2))
		run(toastPolicy("toast_default_case", ""))
	})
}
