package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSArrayConfigBinding(t *testing.T) {
	t.Run("types-and-call-syntax", func(t *testing.T) {
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue("SELECT pg_catalog.array_to_tsvector($1) AS result", "TSVECTOR", "'a' 'b'", "{b,a,a}"))
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigVector("'{b,a,a}'", "'a' 'b'"))
			for _, prefix := range []string{"", "/*é*/ "} {
				ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue(prefix+"SELECT array_to_tsvector(VARIADIC ARRAY['a']) AS result", "TSVECTOR", "'a'"))
			}
			ftsArrayConfigRun(t, p, b, c, fixture.Exec("CREATE DOMAIN array_cfg_texts AS text[]", 0))
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigVector("'{b,a,a}'::array_cfg_texts", "'a' 'b'"))
			ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='simple'", 0))
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue("SELECT pg_catalog.get_current_ts_config() AS result", "REGCONFIG", "simple"))
			for _, tc := range []struct{ call, signature string }{
				{"array_to_tsvector()", "array_to_tsvector()"},
				{"array_to_tsvector('a'::text)", "array_to_tsvector(text)"},
				{"array_to_tsvector(1)", "array_to_tsvector(integer)"},
				{"array_to_tsvector(ARRAY[1])", "array_to_tsvector(integer[])"},
				{"array_to_tsvector('a'::tsvector)", "array_to_tsvector(tsvector)"},
				{"array_to_tsvector(ARRAY['a'],ARRAY['b'])", "array_to_tsvector(text[], text[])"},
				{"array_to_tsvector(lexemes => ARRAY['a'])", "array_to_tsvector(lexemes => text[])"},
				{"get_current_ts_config(1)", "get_current_ts_config(integer)"},
				{"get_current_ts_config(config => 'simple')", "get_current_ts_config(config => unknown)"},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + len([]rune(prefix)))
					ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(prefix+"SELECT "+tc.call, d), true)
				}
			}
			for _, tc := range []struct{ query, message string }{
				{"SELECT array_to_tsvector(DISTINCT ARRAY['a'])", "DISTINCT specified, but array_to_tsvector is not an aggregate function"},
				{"SELECT array_to_tsvector(ARRAY['a'] ORDER BY 1)", "ORDER BY specified, but array_to_tsvector is not an aggregate function"},
				{"SELECT array_to_tsvector(ARRAY['a']) FILTER (WHERE true)", "FILTER specified, but array_to_tsvector is not an aggregate function"},
				{"SELECT get_current_ts_config() FILTER (WHERE true)", "FILTER specified, but get_current_ts_config is not an aggregate function"},
				{"SELECT array_to_tsvector(ARRAY['a']) OVER ()", "OVER specified, but array_to_tsvector is not a window function nor an aggregate function"},
				{"SELECT get_current_ts_config() OVER ()", "OVER specified, but get_current_ts_config is not a window function nor an aggregate function"},
			} {
				d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
				d.Position = "8"
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(tc.query, d), true)
			}
		})
	})
	t.Run("execute-authority-and-recovery", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"array_cfg_actor"})
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { ftsArrayConfigRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE ROLE array_cfg_actor", 0))
			run(fixture.Exec("SET default_text_search_config='simple'", 0))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
				for _, signature := range []string{"array_to_tsvector(text[])", "get_current_ts_config()"} {
					if _, err := c.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+signature+" TO PUBLIC"); err != nil {
						t.Error(err)
					}
				}
			}()
			for _, tc := range []struct {
				signature, name, q, typ string
				want                    any
				args                    []any
			}{
				{"array_to_tsvector(text[])", "array_to_tsvector", "SELECT pg_catalog.array_to_tsvector($1::text[]) AS result", "TSVECTOR", "'a' 'b'", []any{"{b,a,a}"}},
				{"get_current_ts_config()", "get_current_ts_config", "SELECT pg_catalog.get_current_ts_config() AS result", "REGCONFIG", "simple", nil},
			} {
				h := textSearchSettingHold(t, p, c, tc.q)
				defer h.Statement.Close()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+tc.signature+" FROM PUBLIC", 0))
				run(fixture.Exec("SET ROLE array_cfg_actor", 0))
				s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name), tc.args...)
				ftsArrayConfigErrorRoutes(t, p, b, c, s, false)
				s.SelectRows, s.ErrorStage = true, "execute"
				ftsArrayConfigRun(t, p, b, h, s)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+tc.signature+" TO PUBLIC", 0))
				ftsArrayConfigRun(t, p, b, h, ftsArrayConfigValue(tc.q, tc.typ, tc.want, tc.args...))
			}
		})
	})
}

func TestReferenceFTSArrayConfigDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, input, code, message, detail string
	}{
		{"null-element", "{a,NULL}", "22004", "lexeme array may not contain nulls", ""},
		{"empty-element", `{a,""}`, "2200F", "lexeme array may not contain empty strings", ""},
		{"malformed", "oops", "22P02", `malformed array literal: "oops"`, `Array value must start with "{" or dimension information.`},
		{"ragged", "{{a},{b,c}}", "22P02", `malformed array literal: "{{a},{b,c}}"`, "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"bounds-mismatch", "[0:2]={a,b}", "22P02", `malformed array literal: "[0:2]={a,b}"`, "Specified array dimensions do not match array contents."},
		{"reversed-bounds", "[1:0]={}", "2202E", "upper bound cannot be less than lower bound", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				conversion := tc.code == "22P02" || tc.code == "2202E"
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT array_to_tsvector(" + ftsTypesQuote(tc.input) + "::text[])"
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail = tc.detail
					if conversion {
						d.Position = fixture.Position(26 + len([]rune(prefix)))
					}
					ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(q, d), conversion)
				}
				q := "SELECT array_to_tsvector($1::text[]) AS result"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				if conversion {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				s := fixture.ErrorDiagnostic(q, d, tc.input)
				ftsArrayConfigErrorRoutes(t, p, b, c, s, false)
				h := textSearchSettingHold(t, p, c, q)
				defer h.Statement.Close()
				for _, query := range []bool{false, true} {
					s.SelectRows, s.ErrorStage = query, "execute"
					ftsArrayConfigRun(t, p, b, h, s)
				}
				ftsArrayConfigRun(t, p, b, h, ftsArrayConfigValue(q, "TSVECTOR", "'a' 'b'", "{b,a,a}"))
			})
		})
	}
	t.Run("whole-input-consumption", func(t *testing.T) {
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "/*é*/ SELECT array_to_tsvector(ARRAY['a']); garbage"
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
			d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "garbage")])) + 1)
			ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(q, d), true)
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigVector("ARRAY['b','a','a']", "'a' 'b'"))
		})
	})
}
