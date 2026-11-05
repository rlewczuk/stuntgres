package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func sepgsqlContextOrdinaryControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	run(fixture.Exec("CREATE FUNCTION context_identity(value text) RETURNS text LANGUAGE sql VOLATILE STRICT AS 'SELECT value'", 0))
	run(fixture.Exec("CREATE VIEW context_ordinary_view AS SELECT context_identity('view'::text) AS value,context_identity(NULL::text) AS missing", 0))
	// This parsed SQL body and the view retain the renamed declaration's real
	// dependency. The held caller must not re-resolve its obsolete SQL spelling.
	run(fixture.Exec("CREATE FUNCTION context_wrapper(value text) RETURNS text LANGUAGE sql VOLATILE STRICT BEGIN ATOMIC SELECT context_identity(value); END", 0))
	query := "SELECT context_wrapper($1::text) AS value"
	statement, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	check := func() {
		for _, value := range []any{"held", nil} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}, b.Recorder, sepgsqlContextValue(query, value, value))
		}
		run(fixture.Select("SELECT value,missing FROM context_ordinary_view", []string{"value", "missing"}, []string{"TEXT", "TEXT"}, [][]any{{"view", nil}}))
	}
	check()
	run(fixture.Exec("ALTER FUNCTION context_identity(text) RENAME TO context_renamed_identity", 0))
	check()
	sepgsqlContextRoutes(t, p, b, c,
		sepgsqlContextValue("SELECT context_renamed_identity($1::text) AS value", "direct", "direct"),
		sepgsqlContextValue("SELECT context_renamed_identity($1::text) AS value", nil, nil))
}

func sepgsqlContextControlView() fixture.Step {
	return fixture.Select("SELECT subject,changed,outward,inward,missing FROM context_provider_view",
		[]string{"subject", "changed", "outward", "inward", "missing"}, []string{"TEXT", "BOOL", "TEXT", "TEXT", "TEXT"},
		[][]any{{sepgsqlR, true, "Public display label", sepgsqlP, nil}})
}

func TestReferenceSepgsqlSimulationContextControls(t *testing.T) {
	sepgsqlCase(t, sepgsqlContextOrdinaryControls, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		for _, member := range []struct{ name, args string }{{"getcon", ""}, {"setcon", "text"}, {"mcstrans_in", "text"}, {"mcstrans_out", "text"}} {
			run(fixture.Exec("ALTER FUNCTION pg_catalog.sepgsql_"+member.name+"("+member.args+") RENAME TO context_"+member.name, 0))
		}
		for _, query := range []string{
			"CREATE FUNCTION context_subject() RETURNS text LANGUAGE sql VOLATILE AS 'SELECT pg_catalog.context_getcon()'",
			"CREATE FUNCTION context_change(value text) RETURNS boolean LANGUAGE sql VOLATILE AS 'SELECT pg_catalog.context_setcon(value)'",
			"CREATE FUNCTION context_in(value text) RETURNS text LANGUAGE sql VOLATILE STRICT AS 'SELECT pg_catalog.context_mcstrans_in(value)'",
			"CREATE FUNCTION context_out(value text) RETURNS text LANGUAGE sql VOLATILE STRICT AS 'SELECT pg_catalog.context_mcstrans_out(value)'",
			"CREATE VIEW context_provider_view AS SELECT context_subject() AS subject,context_change(context_subject()) AS changed,context_out('" + sepgsqlP + "') AS outward,context_in('Public display label') AS inward,context_in(NULL::text) AS missing",
		} {
			run(fixture.Exec(query, 0))
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run(sepgsqlBool("SELECT pg_catalog.context_setcon('"+sepgsqlR+"') AS changed", "changed", true))
		contexts := func() {
			run(sepgsqlContextValue("SELECT pg_catalog.context_getcon() AS value", sepgsqlR))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, sepgsqlContextValue("SELECT pg_catalog.context_getcon() AS value", sepgsqlA))
		}
		calls := []fixture.Step{
			sepgsqlContextValue("SELECT context_subject() AS value", sepgsqlR),
			fixture.Select("SELECT context_change($1::text) AS changed", []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, sepgsqlR),
			sepgsqlContextValue("SELECT context_out($1::text) AS value", "Public display label", sepgsqlP),
			sepgsqlContextValue("SELECT context_out($1::text) AS value", nil, nil),
			sepgsqlContextValue("SELECT context_in($1::text) AS value", sepgsqlP, "Public display label"),
			sepgsqlContextValue("SELECT context_in($1::text) AS value", nil, nil),
			sepgsqlContextValue("SELECT context_in(context_out($1::text)) AS value", sepgsqlP, sepgsqlP),
			sepgsqlContextControlView(),
		}
		sepgsqlContextCatalog(run, true)
		sepgsqlContextRoutes(t, p, b, c, calls...)
		contexts()
		// Keep actual prepared objects and open immutable rows alive during both
		// real catalog replacements. Controls invalidate statements; only fresh
		// preparations are executed afterward, while old rows/types still work.
		held, err := c.PrepareContext(p.Ctx, "SELECT context_out($1::text) AS value")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		old, err := held.QueryContext(p.Ctx, sepgsqlP)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].Name() != "value" || types[0].DatabaseTypeName() != "TEXT" {
			t.Fatal("original provider metadata", types, err)
		}
		original := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('context_getcon','context_setcon','context_mcstrans_in','context_mcstrans_out','context_subject','context_change','context_in','context_out')", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='context_provider_view'", 0))
		if types[0].DatabaseTypeName() != "TEXT" {
			t.Fatal("retained descriptor mutated")
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(original)); err != nil {
			t.Fatal(err)
		}
		fixture.RangeConsumeHeld(t, old, sepgsqlContextValue("", "Public display label"))
		sepgsqlContextCatalog(run, true)
		sepgsqlContextRoutes(t, p, b, c, calls...)
		contexts()
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		retained := map[string]*sql.Stmt{}
		defer func() {
			for _, statement := range retained {
				statement.Close()
			}
		}()
		for _, step := range calls {
			if retained[step.Query] == nil {
				retained[step.Query], err = c.PrepareContext(p.Ctx, step.Query)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		checkRetained := func() {
			for _, step := range calls {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: retained[step.Query], SQL: step.Query}, b.Recorder, step)
			}
		}
		checkRetained()
		for _, bad := range []string{original + "\nINVALID SQL;", strings.Replace(original, "stuntgres-sepgsql-simulation-v1", "stuntgres-sepgsql-simulation-v999", 1)} {
			if bad == original {
				t.Fatal("malformed control fixture unchanged")
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("malformed restore accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("failed restore changed catalog")
			}
			contexts()
			checkRetained()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore changed catalog", err)
		}
		contexts()
		checkRetained()
		ctx, cancel = context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled reset changed catalog", err)
		}
		contexts()
		checkRetained()
		sepgsqlContextRoutes(t, p, b, c, calls...)
		// NULL still resets the original physical subject through the restored
		// nonstrict wrapper; neither catalog image contained that session state.
		run(sepgsqlBool("SELECT context_change(NULL::text) AS changed", "changed", true))
		run(sepgsqlContextValue("SELECT context_subject() AS value", sepgsqlA))
	})
}

func TestReferenceSepgsqlSimulationContextFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"context_caller"})
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlContextOrdinaryFunctions(t, p, b, c, run)
		sepgsqlContextOrdinaryTransactions(t, p, b, c, run)
		// Use the same genuine ordinary function for the independent ACL smoke.
		run(fixture.Exec("CREATE ROLE context_caller", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO context_caller", 0))
		run(fixture.Exec("GRANT SELECT ON context_inputs TO context_caller", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION context_identity(text) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE context_caller", 0))
		run(fixture.ImplicitError("SELECT context_identity(v) FROM context_inputs", fixture.Diagnostic{Code: "42501", Message: "permission denied for function context_identity"}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("DROP OWNED BY context_caller", 0))
		run(fixture.Exec("DROP ROLE context_caller", 0))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		sepgsqlContextProcess(config, true, sepgsqlA, sepgsqlR)
	}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		check := func(want string) {
			run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", want))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlA))
		}
		check(sepgsqlA)
		query := "SELECT sepgsql_setcon($1::text) AS changed"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, sepgsqlR))
		check(sepgsqlR)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, sepgsqlContextInvalid(query, "unknown", "unknown"))
		check(sepgsqlR)
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, sepgsqlT)
		step.Prepared = true
		run(step)
		check(sepgsqlR)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, nil))
		check(sepgsqlA)
		sepgsqlContextConversions(t, p, b, c)
		check(sepgsqlA)
	})
}
