package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func unaccentDictionaryDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, prepareFailure bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared && prepareFailure {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func unaccentDictionaryNullRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, args ...any) {
	t.Helper()
	unaccentDictionaryText(t, p, b, c, q, nil, args...)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	unaccentDictionaryText(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, nil, args...)
}

func TestReferenceUnaccentDictionaryDiagnostics(t *testing.T) {
	t.Run("strict_typed_nulls", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
			for _, q := range []string{
				"SELECT public.unaccent(NULL::regdictionary,'Hôtel') AS v",
				"SELECT public.unaccent('public.unaccent'::regdictionary,NULL::text) AS v",
				"SELECT public.unaccent(NULL::regdictionary,NULL::text) AS v",
				"SELECT public.unaccent(4294967295::oid::regdictionary,NULL::text) AS v",
				"SELECT public.unaccent(0::oid::regdictionary,NULL::text) AS v",
			} {
				unaccentDictionaryText(t, p, b, c, q, nil)
				danishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			}
			q := "SELECT public.unaccent($1::oid::regdictionary,$2::text) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{int64(4294967295), nil}, {int64(0), nil}, {nil, "Hôtel"}} {
				unaccentDictionaryText(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, nil, args...)
			}
		})
	})
	for _, n := range []int64{0, 4294967295} {
		t.Run(fmt.Sprintf("missing_oid_%d", n), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
				d := dictionaryDiagnostic("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", n))
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, fmt.Sprintf("SELECT public.unaccent(%d::oid::regdictionary,'Hôtel')", n), d, false)
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, "SELECT public.unaccent($1::oid::regdictionary,$2::text)", d, false, n, "Hôtel")
			})
		})
	}
	for _, input := range []string{"unaccent_missing", "unaccent_absent.pick", "a..b"} {
		for _, nullText := range []bool{false, true} {
			t.Run(fmt.Sprintf("input_%s_null_%t", input, nullText), func(t *testing.T) {
				dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
					code, primary := "42704", `text search dictionary "`+input+`" does not exist`
					if input == "a..b" {
						code, primary = "42602", "invalid name syntax"
					}
					text := "'Hôtel'::text"
					var arg any = "Hôtel"
					if nullText {
						text, arg = "NULL::text", nil
					}
					q := "SELECT public.unaccent(" + regtsLiteral(input) + "::regdictionary," + text + ")"
					d := dictionaryDiagnostic(code, primary)
					d.Position = "24"
					unaccentDictionaryDiagnosticRoutes(t, p, b, c, q, d, true)
					// Unknown/REGDICTIONARY parameters perform input conversion at Bind,
					// unlike already-typed numeric OIDs and runtime TEXT casts.
					q = "SELECT public.unaccent($1,$2)"
					d.Position, d.Context = "", "unnamed portal parameter $1 = '...'"
					unaccentDictionaryDiagnosticRoutes(t, p, b, c, q, d, false, input, arg)
					q = "SELECT public.unaccent($1::text::regdictionary,$2::text)"
					d.Context = ""
					if nullText {
						// A runtime TEXT cast is not input conversion at Bind:
						// the strict call can return NULL without converting it.
						unaccentDictionaryNullRoutes(t, p, b, c, q+" AS v", input, arg)
					} else {
						unaccentDictionaryDiagnosticRoutes(t, p, b, c, q, d, false, input, arg)
					}
				})
			})
		}
	}
	for _, tc := range []struct{ q, signature string }{
		{"SELECT public.unaccent()", ""},
		{"SELECT public.unaccent(*)", ""},
		{"SELECT public.unaccent('simple','Hôtel') WITHIN GROUP (ORDER BY 1)", "unknown, unknown, integer"},
		{"SELECT public.unaccent('simple','Hôtel','extra')", "unknown, unknown, unknown"},
		{"SELECT public.unaccent(true,'Hôtel')", "boolean, unknown"},
		{"SELECT public.unaccent('simple',true)", "unknown, boolean"},
		{"SELECT public.unaccent('simple'::text,'Hôtel')", "text, unknown"},
		{"SELECT public.unaccent(dictionary=>'simple',token=>'Hôtel')", "dictionary => unknown, token => unknown"},
		{"SELECT public.unaccent(VARIADIC ARRAY['simple','Hôtel'])", "text[]"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
				d := dictionaryDiagnostic("42883", "function public.unaccent("+tc.signature+") does not exist")
				d.Position, d.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, tc.q, d, true)
			})
		})
	}
	for _, tc := range []struct{ q, code, primary string }{
		{"SELECT public.unaccent(DISTINCT 'simple','Hôtel')", "42809", "DISTINCT specified, but public.unaccent is not an aggregate function"},
		{"SELECT public.unaccent('simple','Hôtel') FILTER (WHERE true)", "42809", "FILTER specified, but public.unaccent is not an aggregate function"},
		{"SELECT public.unaccent('simple','Hôtel') OVER ()", "42809", "OVER specified, but public.unaccent is not a window function nor an aggregate function"},
		{"SELECT public.unaccent('simple','Hôtel' ORDER BY 1)", "42809", "ORDER BY specified, but public.unaccent is not an aggregate function"},
		{"SELECT public.unaccent(" + strings.Repeat("'x',", 100) + "'x')", "54023", "cannot pass more than 100 arguments to a function"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
				d := dictionaryDiagnostic(tc.code, tc.primary)
				d.Position = "8"
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, tc.q, d, true)
			})
		})
	}
	for _, name := range []string{"unaccent", "public.unaccent"} {
		t.Run("not_installed_"+name, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := dictionaryDiagnostic("42883", "function "+name+"(regdictionary, unknown) does not exist")
				d.Position, d.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, "SELECT "+name+"('simple'::regdictionary,'Hôtel')", d, true)
			})
		})
	}
	t.Run("argument_error_order", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
			for _, q := range []string{
				"SELECT public.unaccent('unaccent_missing'::regdictionary,('bad'::integer)::text)",
				"SELECT public.unaccent(('bad'::integer)::regdictionary,'unaccent_missing'::integer::text)",
			} {
				d := dictionaryDiagnostic("42704", `text search dictionary "unaccent_missing" does not exist`)
				d.Position = "24"
				if strings.HasPrefix(q, "SELECT public.unaccent((") {
					d = dictionaryDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
					d.Position = "25"
				}
				unaccentDictionaryDiagnosticRoutes(t, p, b, c, q, d, true)
			}
		})
	})
	for _, dictionaryType := range []string{"regdictionary", "text"} {
		t.Run("sql_prepare_"+dictionaryType, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
				regtsExec(t, p, b, c, "PREPARE unaccent_diagnostic("+dictionaryType+",text) AS SELECT public.unaccent($1::regdictionary,$2) AS v", 0)
				unaccentDictionaryText(t, p, b, c, "EXECUTE unaccent_diagnostic('public.unaccent','Hôtel')", "Hotel")
				for _, text := range []string{"'Hôtel'", "NULL"} {
					q := "EXECUTE unaccent_diagnostic('unaccent_missing'," + text + ")"
					if dictionaryType == "text" && text == "NULL" {
						unaccentDictionaryNullRoutes(t, p, b, c, q)
						continue
					}
					d := dictionaryDiagnostic("42704", `text search dictionary "unaccent_missing" does not exist`)
					if dictionaryType == "regdictionary" {
						d.Position = fixture.Position(strings.Index(q, "'unaccent_missing'") + 1)
					}
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				unaccentDictionaryText(t, p, b, c, "EXECUTE unaccent_diagnostic('pg_catalog.simple','WOrD')", "word")
				regtsExec(t, p, b, c, "DEALLOCATE unaccent_diagnostic", 0)
			})
		})
	}
	t.Run("schema_usage", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE SCHEMA dictionary_owned", "CREATE ROLE dictionary_peer", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.hidden (TEMPLATE=pg_catalog.simple)", "SET ROLE dictionary_peer"} {
				regtsExec(t, p, b, c, q, 0)
			}
			defer regtsExec(t, p, b, c, "RESET ROLE", 0)
			d := dictionaryDiagnostic("42501", "permission denied for schema dictionary_owned")
			d.Position = "24"
			unaccentDictionaryDiagnosticRoutes(t, p, b, c, "SELECT public.unaccent('dictionary_owned.hidden'::regdictionary,'WOrD')", d, true)
			regtsExec(t, p, b, c, "SET search_path=dictionary_owned,public", 0)
			d = dictionaryDiagnostic("42704", `text search dictionary "hidden" does not exist`)
			d.Position = "24"
			unaccentDictionaryDiagnosticRoutes(t, p, b, c, "SELECT public.unaccent('hidden'::regdictionary,'WOrD')", d, true)
		})
	})
}
