package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func externalLanguageScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, p, b, c)
			if len(notices) != 0 {
				t.Errorf("unexpected external-language notices: %#v", notices)
			}
		})
	}
}

func externalLanguageError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, query bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		stage := "execute"
		var err error
		if prepared {
			var stmt *sql.Stmt
			stmt, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				stage = "prepare"
			} else {
				if query {
					var rows *sql.Rows
					rows, err = stmt.QueryContext(p.Ctx)
					if err == nil {
						for rows.Next() {
						}
						err = rows.Err()
						rows.Close()
					}
				} else {
					_, err = stmt.ExecContext(p.Ctx)
				}
				stmt.Close()
			}
		} else if query {
			var rows *sql.Rows
			rows, err = c.QueryContext(p.Ctx, q)
			if err == nil {
				for rows.Next() {
				}
				err = rows.Err()
				rows.Close()
			}
		} else {
			_, err = c.ExecContext(p.Ctx, q)
		}
		if err == nil {
			t.Errorf("expected rejection, prepared=%v: %s", prepared, q)
			continue
		}
		wantStage := "execute"
		if prepared && (query || want.Code == "42601") {
			wantStage = "prepare"
		}
		fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: wantStage}, stage)
	}
}

func externalLanguageMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name string) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("language %q does not exist", name))
	externalLanguageError(t, p, b, c, q, d, false)
}

func externalLanguageMissingCall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"(integer) does not exist")
	d.Position = "8"
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	externalLanguageError(t, p, b, c, "SELECT "+name+"(4)", d, true)
}

func externalLanguageCapability(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, language string) {
	t.Helper()
	externalLanguageError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("0A000", "LANGUAGE "+language+" functions are unsupported in embedded mode"), false)
}

func externalLanguageInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	want := [][]any{{"internal"}}
	if b.PG {
		want = [][]any{{"c"}, {"internal"}, {"plpgsql"}, {"sql"}}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT lanname FROM pg_catalog.pg_language ORDER BY lanname", []string{"lanname"}, []string{"NAME"}, want))
}

func TestReferenceRoutineExternalLanguagesDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ext_reader"})
	fixture.SchemaReferenceCleanup(t, "ext_denied")
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageInventory(t, p, b, c)
		for _, language := range []string{"pltcl", "pltclu", "plperl", "plperlu", "plpython3u", "plpython", "plpythonu", "ext_absent", "\"Ext_Mixed\"", "EXT_UPPER"} {
			name := strings.Trim(language, "\"")
			if language == "EXT_UPPER" {
				name = "ext_upper"
			}
			for _, procedure := range []bool{false, true} {
				prefix := "CREATE FUNCTION ext_missing(integer) RETURNS integer "
				if procedure {
					prefix = "CREATE PROCEDURE ext_missing(integer) "
				}
				for _, body := range []string{"LANGUAGE " + language + " AS $$SELECT 5$$", "AS 'SELECT 5' LANGUAGE " + language} {
					externalLanguageMissing(t, p, b, c, prefix+body, name)
				}
			}
		}
		externalLanguageMissing(t, p, b, c, "CREATE FUNCTION ext_bad_type(ext_no_type) RETURNS integer LANGUAGE pltcl AS 'SELECT 5'", "pltcl")
		q := "CREATE FUNCTION ext_no_schema.f() RETURNS integer LANGUAGE pltcl AS 'SELECT 5'"
		d := fixture.ControlsReferenceDiagnostic("3F000", `schema "ext_no_schema" does not exist`)
		externalLanguageError(t, p, b, c, q, d, false)
		for _, tc := range []struct{ q, message string }{
			{"CREATE FUNCTION ext_two() RETURNS integer LANGUAGE internal AS 'int4abs',''", `only one AS item needed for language "internal"`},
			{"CREATE FUNCTION ext_two_sql() RETURNS integer LANGUAGE SQL AS 'SELECT 5',''", `only one AS item needed for language "sql"`},
			{"CREATE FUNCTION ext_inline() RETURNS integer LANGUAGE internal RETURN 5", "inline SQL function body only valid for language SQL"},
			{"CREATE PROCEDURE ext_inline_proc() LANGUAGE internal BEGIN ATOMIC SELECT 5; END", "inline SQL function body only valid for language SQL"},
		} {
			externalLanguageError(t, p, b, c, tc.q, fixture.ControlsReferenceDiagnostic("42P13", tc.message), false)
		}
		routineExec(t, p, b, c, "CREATE ROLE ext_reader", "CREATE SCHEMA ext_denied", "GRANT USAGE, CREATE ON SCHEMA public TO ext_reader", "SET SESSION AUTHORIZATION ext_reader")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		externalLanguageError(t, p, b, c, "CREATE FUNCTION ext_denied.f() RETURNS integer LANGUAGE pltcl AS 'SELECT 5'", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ext_denied"), false)
		for _, language := range []string{"c", "internal"} {
			for _, q := range []string{
				"CREATE FUNCTION ext_denied_runtime(integer) RETURNS integer LANGUAGE " + language + " AS 'missing'",
				"CREATE PROCEDURE ext_denied_proc() AS 'missing','symbol' LANGUAGE " + language,
				"CREATE FUNCTION ext_denied_body() RETURNS integer LANGUAGE " + language + " RETURN 5",
			} {
				externalLanguageError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for language "+language), false)
			}
		}
	})
}

func TestReferenceRoutineExternalLanguagesNativeBoundary(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ext_symbols")
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageInventory(t, p, b, c)
		for i, body := range []string{"LANGUAGE internal AS 'int4abs'", "AS $$int4abs$$ LANGUAGE internal"} {
			name := fmt.Sprintf("ext_internal_%d", i)
			q := "CREATE FUNCTION " + name + "(integer) RETURNS integer " + body
			if b.PG {
				routineExec(t, p, b, c, q)
				routineValue(t, p, b, c, "SELECT "+name+"(-5) AS v", "v", "INT4", int64(5))
			} else {
				externalLanguageCapability(t, p, b, c, q, "internal")
				externalLanguageMissingCall(t, p, b, c, name)
			}
		}
		routineExec(t, p, b, c, "CREATE SCHEMA ext_symbols")
		for _, q := range []string{
			"CREATE FUNCTION ext_c_handler() RETURNS language_handler AS '$libdir/plpgsql','plpgsql_call_handler' LANGUAGE c",
			"CREATE FUNCTION ext_c_handler_order() RETURNS language_handler LANGUAGE c AS '$libdir/plpgsql',$$plpgsql_call_handler$$",
			"CREATE FUNCTION ext_symbols.plpgsql_call_handler() RETURNS language_handler LANGUAGE c AS '$libdir/plpgsql'",
			"CREATE PROCEDURE ext_c_proc() LANGUAGE c AS '$libdir/plpgsql','plpgsql_call_handler'",
		} {
			if b.PG {
				routineExec(t, p, b, c, q)
			} else {
				externalLanguageCapability(t, p, b, c, q, "c")
			}
		}
		if !b.PG {
			for _, body := range []string{"AS ''", "AS 'missing'", "AS 'missing',''", "AS '', 'missing'"} {
				externalLanguageCapability(t, p, b, c, "CREATE FUNCTION ext_empty() RETURNS integer LANGUAGE c "+body, "c")
			}
			externalLanguageCapability(t, p, b, c, "CREATE FUNCTION ext_default() RETURNS integer LANGUAGE internal AS ''", "internal")
		}
		externalLanguageInventory(t, p, b, c)
	})
}
