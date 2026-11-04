package reference_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sepgsqlInitializationBodies(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	admin := func() { run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true)) }
	for _, query := range []string{
		"CREATE FUNCTION init_inner() RETURNS text AS '$libdir/sepgsql','sepgsql_getcon' LANGUAGE C VOLATILE",
		"CREATE FUNCTION init_sql_body() RETURNS text LANGUAGE sql VOLATILE AS 'SELECT init_inner()'",
		"CREATE FUNCTION init_pl_body() RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN init_inner(); END$$",
		"CREATE FUNCTION init_body_error(v integer) RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 12 / v'",
		"CREATE FUNCTION init_body_caught(v integer) RETURNS text LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM init_body_error(v); EXCEPTION WHEN division_by_zero THEN RETURN init_inner(); END; RETURN 'success'; END$$",
		"CREATE FUNCTION init_phase_caught() RETURNS text LANGUAGE plpgsql AS $$BEGIN PERFORM init_sql_body(); BEGIN RETURN 'leaked/' || init_inner(); EXCEPTION WHEN insufficient_privilege THEN RETURN 'caught/' || sepgsql_getcon(); END; END$$",
		"CREATE FUNCTION init_wait() RETURNS void LANGUAGE sql VOLATILE AS 'SELECT pg_sleep(30)'",
	} {
		run(fixture.Exec(query, 0))
	}
	sepgsqlInitializationLabel(run, "init_inner()", sepgsqlInitInner)
	for _, signature := range []string{"init_sql_body()", "init_pl_body()", "init_body_error(integer)", "init_body_caught(integer)", "init_wait()"} {
		sepgsqlInitializationLabel(run, signature, sepgsqlS)
	}
	for _, prepared := range []bool{false, true} {
		run(sepgsqlSet(sepgsqlR))
		for _, name := range []string{"init_sql_body", "init_pl_body"} {
			step := sepgsqlText("SELECT "+name+"() AS value", "value", sepgsqlT)
			step.Prepared, step.CheckMetadata = prepared, sepgsqlInitializationTextMetadata()
			run(step)
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		// Successful T-only initialization in one nested SQL frame cannot be
		// adopted by the later R RETURN-expression frame for the same function.
		step := sepgsqlText("SELECT init_phase_caught() AS value", "value", "caught/"+sepgsqlR)
		step.Prepared = prepared
		run(step)
		denied := sepgsqlDenied("SELECT init_inner()")
		denied.Prepared, denied.SelectRows = prepared, true
		run(denied)
		failure := fixture.ImplicitError("SELECT init_body_error($1)", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "SQL function \"init_body_error\" statement 1"}, int64(0))
		failure.Prepared, failure.SelectRows = prepared, true
		run(failure)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		step = sepgsqlText("SELECT init_body_caught(0) AS value", "value", sepgsqlT)
		step.Prepared = prepared
		run(step)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		// Fresh body owners still work after caught and uncaught failures.
		run(sepgsqlText("SELECT init_sql_body() AS value", "value", sepgsqlT))
		admin()
	}

	const wait = "SELECT init_wait()"
	held := fixture.IdentityHeld(t, p.Ctx, c, wait)
	for _, prepared := range []bool{false, true} {
		run(sepgsqlSet(sepgsqlR))
		// The fixture observes a real active sleep and joins the canceled
		// operation, rather than proving only a pre-canceled request.
		fixture.SleepActiveCancelWithContext(t, p, b, c, wait, "SQL function \"init_wait\" statement 1", nil, func(ctx context.Context) fixture.RelationOutcome {
			var result sql.Result
			var err error
			if prepared {
				result, err = held.Statement.ExecContext(ctx)
			} else {
				result, err = c.ExecContext(ctx, wait)
			}
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlText("SELECT init_pl_body() AS value", "value", sepgsqlT))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
	}
}

func sepgsqlInitializationConditional(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	admin := func() { run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true)) }
	run(fixture.Exec("CREATE TABLE init_conditional(id integer,n numeric)", 0))
	run(fixture.Exec("INSERT INTO init_conditional VALUES(1,NULL),(2,NULL)", 2))
	sepgsqlInitializationLabel(run, "pg_catalog.round(numeric,integer)", sepgsqlInitInner)
	const query = "SELECT id,(round(n))::text AS value FROM init_conditional ORDER BY id"
	held := fixture.IdentityHeld(t, p.Ctx, c, query)
	missing := fixture.Select(query, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}})
	run(sepgsqlSet(sepgsqlR))
	run(missing)
	run(fixture.Prepared(missing))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, missing)
	admin()
	run(fixture.Exec("INSERT INTO init_conditional VALUES(3,3.14)", 1))
	run(sepgsqlSet(sepgsqlR))
	// The same genuine row-derived call now reaches a native SQL body. Its
	// helper must initialize under R and deny, not use a closed worker's
	// nonexistent initialization or an earlier all-NULL execution's owner.
	denied := sepgsqlDenied(query)
	denied.SelectRows = true
	denied.Diagnostic.Context = "SQL function \"round\" statement 1"
	run(denied)
	run(fixture.Prepared(denied))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	admin()
	sepgsqlInitializationLabel(run, "pg_catalog.round(numeric)", sepgsqlS)
	run(sepgsqlSet(sepgsqlR))
	entered := fixture.Select(query, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), "3"}})
	run(entered)
	run(fixture.Prepared(entered))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, entered)
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	admin()
	// Restore builtin labels before the unrelated arithmetic graph. No broad
	// serial gate is justified merely because a simulation is registered.
	sepgsqlInitializationLabel(run, "pg_catalog.round(numeric)", sepgsqlP)
	sepgsqlInitializationLabel(run, "pg_catalog.round(numeric,integer)", sepgsqlP)
	sepgsqlInitializationArithmetic(run, true)
}

// Assert actual entered owners without imposing a stock audit-event count or
// ordering. Used by attachment tests whose results alone could hide a missed
// predicate/key/WHEN callback.
func sepgsqlInitializationEntered(t *testing.T, audit *sepgsqlInitializationAudit, names ...string) {
	t.Helper()
	events := audit.snapshot()
	for _, name := range names {
		found := false
		for _, event := range events {
			if event.Subject == sepgsqlR && event.Target == sepgsqlS && event.Class == "db_procedure" && strings.HasSuffix(event.Name, "."+name) && slices.Contains(event.Required, "entrypoint") && len(event.Denied) == 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing actual trusted attachment %s: %#v", name, events)
		}
	}
}
