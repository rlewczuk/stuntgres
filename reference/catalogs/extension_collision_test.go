package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Expected complete helper Context records are independently grounded in the
// REL_18_0 install script. Root's selected pinned run confirms these additions.
func extensionHelperCollisionOracle(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var ns int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, args, result, symbol, properties string
		line                                   int
	}{
		{"unaccent", "regdictionary, text", "text", "unaccent_dict", "STABLE STRICT PARALLEL SAFE", 6},
		{"unaccent_init", "internal", "internal", "unaccent_init", "PARALLEL SAFE", 16},
		{"unaccent_lexize", "internal,internal,internal,internal", "internal", "unaccent_lexize", "PARALLEL SAFE", 21},
	} {
		setup := fmt.Sprintf("CREATE FUNCTION public.%s(%s) RETURNS %s AS '$libdir/unaccent', '%s' LANGUAGE C %s", tc.name, tc.args, tc.result, tc.symbol, tc.properties)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(setup, 0))
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname=$1 AND pronamespace=$2", tc.name, ns).Scan(&original); err != nil {
			t.Fatal(err)
		}
		_, err := c.ExecContext(p.Ctx, "CREATE EXTENSION unaccent")
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, tc.name), Context: fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS '$libdir/unaccent', '%s'\n\tLANGUAGE C %s\"\nextension script file \"unaccent--1.1.sql\", near line %d", tc.name, tc.args, tc.result, tc.symbol, tc.properties, tc.line)}
		fixture.AssertDiagnostic(t, err, &want)
		extensionCount(t, p, c, r, 0)
		var count, after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_proc WHERE pronamespace=$1 AND (proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize')", ns).Scan(&count); err != nil || count != 1 {
			t.Fatal("preceding functions leaked", count, err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname=$1 AND pronamespace=$2", tc.name, ns).Scan(&after); err != nil || after != original {
			t.Fatal("loose function changed", original, after, err)
		}
		for _, table := range []string{"pg_ts_template", "pg_ts_dict"} {
			nameColumn := "tmplname"
			nsColumn := "tmplnamespace"
			if table == "pg_ts_dict" {
				nameColumn, nsColumn = "dictname", "dictnamespace"
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM "+table+" WHERE "+nameColumn+"='unaccent' AND "+nsColumn+"=$1", ns).Scan(&count); err != nil || count != 0 {
				t.Fatal("later members leaked", table, count, err)
			}
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(fmt.Sprintf("DROP FUNCTION public.%s(%s)", tc.name, tc.args), 0))
	}
}
func extensionOracleCollisionInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) {
	t.Helper()
	var ns int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, query string }{
		{"", "SELECT count(*) FROM pg_proc WHERE pronamespace=$1 AND (proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize')"},
		{"pg_ts_template", "SELECT count(*) FROM pg_ts_template WHERE tmplnamespace=$1 AND tmplname='unaccent'"},
		{"pg_ts_dict", "SELECT count(*) FROM pg_ts_dict WHERE dictnamespace=$1 AND dictname='unaccent'"},
	} {
		var want int64
		if table == tc.kind {
			want = 1
		}
		var count int64
		if err := c.QueryRowContext(p.Ctx, tc.query, ns).Scan(&count); err != nil || count != want {
			t.Fatal("failed installation member inventory", tc.query, count, want, err)
		}
	}
}
