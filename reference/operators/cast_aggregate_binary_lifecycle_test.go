package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

const castBinaryValueQuery = "SELECT encode(cab_scope.bytes(v ORDER BY id),'hex') AS initialized,encode(cab_scope.seed(v ORDER BY id),'hex') AS seeded FROM (VALUES(1,$1::text),(2,$2::text),(3,NULL::text)) s(id,v)"

func castBinarySetup(t *testing.T, ctx context.Context, ex fixture.Executor, b fixture.Backend) {
	t.Helper()
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA cab_scope", 0),
		fixture.Exec("CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT", 0),
		fixture.Exec("CREATE AGGREGATE cab_scope.bytes(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea,INITCOND='')", 0),
		fixture.Exec("CREATE AGGREGATE cab_scope.seed(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea)", 0),
		fixture.Exec("CREATE TABLE cab_scope.input(id integer,v text)", 0),
		fixture.Exec(`INSERT INTO cab_scope.input VALUES(1,NULL),(2,'a'),(3,'b'),(4,'c'),(5,NULL),(6,'\x4142')`, 6),
		fixture.Exec("CREATE VIEW cab_scope.saved AS SELECT encode(cab_scope.bytes(v ORDER BY id),'hex') AS initialized FROM cab_scope.input", 0),
		fixture.Exec("CREATE VIEW cab_scope.seed_saved AS SELECT encode(cab_scope.seed(v ORDER BY id),'hex') AS seeded FROM cab_scope.input", 0),
	} {
		fixture.RunStep(t, ctx, ex, b.Recorder, step)
	}
}

func castBinaryLifecycle(t *testing.T, ctx context.Context, b fixture.Backend, final string) {
	t.Helper()
	conn, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connOpen := true
	defer func() {
		if connOpen {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	castBinarySetup(t, ctx, conn, b)
	stmt, err := conn.PrepareContext(ctx, castBinaryValueQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	}()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: castBinaryValueQuery}
	// The same held statement must cover all five first-state cases and the
	// post-transition startup rejection, not a replacement preparation.
	for _, tc := range []struct{ first, second, initialized, seeded any }{
		{"a", "bc", "616263", "616263"},
		{`\x4142`, "", "5c7834313432", "5c7834313432"},
		{nil, "abc", "616263", "616263"},
		{"", "", "", ""},
		{nil, nil, "", nil},
	} {
		step := fixture.Select(castBinaryValueQuery, []string{"initialized", "seeded"}, []string{"TEXT", "TEXT"}, [][]any{{tc.initialized, tc.seeded}}, tc.first, tc.second)
		fixture.RunStep(t, ctx, conn, b.Recorder, step)
		fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Prepared(step))
		fixture.RunStep(t, ctx, held, b.Recorder, step)
	}
	for _, step := range castAggregateBinaryChecks("binary") {
		fixture.RunStep(t, ctx, conn, b.Recorder, step)
	}
	fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Exec("DROP CAST (text AS bytea) RESTRICT", 0))
	if final == "inout" {
		fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Exec("CREATE CAST (text AS bytea) WITH INOUT AS IMPLICIT", 0))
	}
	failure := fixture.ImplicitError(castBinaryValueQuery, castAggregateSeedDiagnostic(t, ctx, conn), "a", "bc")
	failure.SelectRows, failure.ErrorStage = true, "execute"
	fixture.RunStep(t, ctx, held, b.Recorder, failure)
	castBinaryFinal(t, ctx, conn, b, final)
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	connOpen = false
}

func castBinaryFinal(t *testing.T, ctx context.Context, ex fixture.Executor, b fixture.Backend, final string) {
	t.Helper()
	for _, step := range castAggregateBinaryChecks(final) {
		fixture.RunStep(t, ctx, ex, b.Recorder, step)
		fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Prepared(step))
	}
	if final == "inout" {
		step := fixture.Select(`SELECT encode(('\x4142'::text)::bytea,'hex') AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"4142"}})
		fixture.RunStep(t, ctx, ex, b.Recorder, step)
		fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Prepared(step))
	}
	// Resolve the actual seed identity on every replay owner, never the source
	// allocator's identity or a cached error from before Restore.
	diagnostic := castAggregateSeedDiagnostic(t, ctx, ex)
	for _, query := range []string{
		"SELECT encode(cab_scope.seed(v),'hex') FROM cab_scope.input",
		"SELECT encode(cab_scope.seed(v),'hex') FROM cab_scope.input WHERE FALSE",
		"SELECT seeded FROM cab_scope.seed_saved",
	} {
		step := fixture.ImplicitError(query, diagnostic)
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(t, ctx, ex, b.Recorder, step)
		fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Prepared(step))
	}
	code, message := "42883", "function pg_catalog.byteacat(bytea, text) does not exist"
	if final == "inout" {
		code, message = "42804", "function pg_catalog.byteacat(bytea, bytea) requires run-time type coercion"
	}
	for _, seed := range []string{",INITCOND=''", ""} {
		step := fixture.ImplicitError("CREATE AGGREGATE cab_scope.fresh(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea"+seed+")", fixture.Diagnostic{Code: code, Message: message})
		step.ErrorStage = "execute"
		for _, prepared := range []bool{false, true} {
			step.Prepared = prepared
			fixture.RunStep(t, ctx, ex, b.Recorder, step)
			fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='fresh'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_aggregate a JOIN pg_proc p ON a.aggfnoid=p.oid JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='fresh'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	}
}
