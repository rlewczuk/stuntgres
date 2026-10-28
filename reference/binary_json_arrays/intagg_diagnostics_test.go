package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// intaggError asserts every portable field and the actual failure stage on both
// public routes. Bound-input and utility errors occur after successful Prepare.
func intaggError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, runtime bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := fixture.ImplicitError(q, want, args...)
			s.SelectRows = strings.HasPrefix(q, "SELECT")
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared && !runtime {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func intaggMissing(signature string) fixture.Diagnostic {
	return fixture.Diagnostic{
		Code: "42883", Message: "function " + signature + " does not exist",
		Hint:     "No function matches the given name and argument types. You might need to add explicit type casts.",
		Position: "8",
	}
}

func TestReferenceIntaggInstalledIdentity(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ q, signature string }{
			{"SELECT int_array_aggregate(2)", "int_array_aggregate(integer)"},
			{"SELECT int_array_enum('{2}'::integer[])", "int_array_enum(integer[])"},
		} {
			t.Run("absent-"+tc.q, func(t *testing.T) { intaggError(t, p, b, c, tc.q, intaggMissing(tc.signature), false) })
		}
		intaggInstall(run)
		fixture.ArrayAggBoth(run, fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='intagg'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"intagg", "1.1", true}}))
		fixture.ArrayAggBoth(run, fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='intagg' AND version='1.1'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.1", true, false, true}}))
		t.Run("duplicate-install", func(t *testing.T) {
			intaggError(t, p, b, c, "CREATE EXTENSION intagg SCHEMA ia_addon", fixture.Diagnostic{Code: "42710", Message: `extension "intagg" already exists`}, true)
		})
		fixture.ArrayAggBoth(run, fixture.Select("SELECT p.proname,p.prorettype::bigint AS prorettype,p.proisstrict,p.provolatile FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ia_addon' AND p.proname IN ('int_array_aggregate','int_array_enum') ORDER BY p.proname", []string{"proname", "prorettype", "proisstrict", "provolatile"}, []string{"NAME", "INT8", "BOOL", "CHAR"}, [][]any{{"int_array_aggregate", int64(1007), false, "i"}, {"int_array_enum", int64(23), true, "i"}}))
		if b.PG {
			// Upstream corroboration only: native intentionally does not expose
			// pg_proc.proretset. Shared cursor metadata proves the SRF contract.
			fixture.ArrayAggBoth(run, fixture.Select("SELECT p.proname,p.proretset FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ia_addon' AND p.proname IN ('int_array_aggregate','int_array_enum') ORDER BY p.proname", []string{"proname", "proretset"}, []string{"NAME", "BOOL"}, [][]any{{"int_array_aggregate", false}, {"int_array_enum", true}}))
		}
		fixture.ArrayAggBoth(run, fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='intagg' AND d.deptype='e' AND p.proname IN ('int_array_aggregate','int_array_enum')", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		for _, tc := range []struct{ q, signature string }{
			{"SELECT int_array_aggregate(2)", "int_array_aggregate(integer)"},
			{"SELECT int_array_enum('{2}'::integer[])", "int_array_enum(integer[])"},
			{"SELECT pg_catalog.int_array_aggregate(2)", "pg_catalog.int_array_aggregate(integer)"},
			{"SELECT pg_catalog.int_array_enum('{2}'::integer[])", "pg_catalog.int_array_enum(integer[])"},
			{"SELECT public.int_array_enum('{2}'::integer[])", "public.int_array_enum(integer[])"},
			{"SELECT ia_addon.\"INT_ARRAY_ENUM\"('{2}'::integer[])", "ia_addon.INT_ARRAY_ENUM(integer[])"},
		} {
			t.Run("off-path-"+tc.q, func(t *testing.T) { intaggError(t, p, b, c, tc.q, intaggMissing(tc.signature), false) })
		}
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT ia_addon.\"int_array_aggregate\"(2)", []string{"int_array_aggregate"}, []string{"_INT4"}, [][]any{{"{2}"}}))
		run(fixture.Exec("SET search_path=ia_addon,public", 0))
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT int_array_aggregate(2)", []string{"int_array_aggregate"}, []string{"_INT4"}, [][]any{{"{2}"}}))
		visible := fixture.Select("SELECT int_array_enum('{2,2,NULL}')", []string{"int_array_enum"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}})
		visible.Bag = true
		fixture.ArrayAggBoth(run, visible)
		if !b.PG {
			run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ia_addon'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
	})
}

func TestReferenceIntaggDiagnostics(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggInstall(run)
		intaggInput(run)
		// Full records observed on the pinned PostgreSQL18.0 oracle.
		for _, tc := range []struct{ call, signature string }{
			{"int_array_aggregate()", "int_array_aggregate()"},
			{"int_array_aggregate(*)", "int_array_aggregate()"},
			{"int_array_aggregate(v,id)", "int_array_aggregate(integer, integer)"},
			{"int_array_aggregate(v::bigint)", "int_array_aggregate(bigint)"},
			{"int_array_aggregate(v::numeric)", "int_array_aggregate(numeric)"},
			{"int_array_aggregate(v::text)", "int_array_aggregate(text)"},
			{"int_array_aggregate(x=>v)", "int_array_aggregate(x => integer)"},
			{"int_array_aggregate(VARIADIC ARRAY[v])", "int_array_aggregate(integer[])"},
			{"int_array_enum()", "int_array_enum()"},
			{"int_array_enum(*)", "int_array_enum()"},
			{"int_array_enum(ARRAY[v],ARRAY[id])", "int_array_enum(integer[], integer[])"},
			{"int_array_enum(ARRAY['x']::text[])", "int_array_enum(text[])"},
			{"int_array_enum(ARRAY[1]::bigint[])", "int_array_enum(bigint[])"},
			{"int_array_enum(v)", "int_array_enum(integer)"},
			{"int_array_enum(x=>ARRAY[v])", "int_array_enum(x => integer[])"},
			{"int_array_aggregate(ia_addon.int_array_aggregate(v))", "int_array_aggregate(integer[])"},
			{"int_array_aggregate(v::text) OVER()", "int_array_aggregate(text)"},
		} {
			q := "SELECT ia_addon." + tc.call + " FROM ia_input"
			t.Run(tc.call, func(t *testing.T) { intaggError(t, p, b, c, q, intaggMissing("ia_addon."+tc.signature), false) })
		}
		for _, tc := range []struct{ q, code, message, position, detail string }{
			{"SELECT ia_addon.int_array_aggregate('bad') FROM ia_input", "22P02", `invalid input syntax for type integer: "bad"`, "37", ""},
			{"SELECT ia_addon.int_array_aggregate('2147483648') FROM ia_input", "22003", `value "2147483648" is out of range for type integer`, "37", ""},
			{"SELECT ia_addon.int_array_enum('{x}') FROM ia_input", "22P02", `invalid input syntax for type integer: "x"`, "32", ""},
			{"SELECT ia_addon.int_array_enum('{1,') FROM ia_input", "22P02", `malformed array literal: "{1,"`, "32", "Unexpected end of input."},
			{"SELECT ia_addon.int_array_enum(DISTINCT ARRAY[v]) FROM ia_input", "42809", "DISTINCT specified, but ia_addon.int_array_enum is not an aggregate function", "8", ""},
			{"SELECT ia_addon.int_array_enum(ARRAY[v] ORDER BY id) FROM ia_input", "42809", "ORDER BY specified, but ia_addon.int_array_enum is not an aggregate function", "8", ""},
			{"SELECT ia_addon.int_array_enum(ARRAY[v]) FILTER(WHERE TRUE) FROM ia_input", "42809", "FILTER specified, but ia_addon.int_array_enum is not an aggregate function", "8", ""},
			{"SELECT ia_addon.int_array_enum(ARRAY[v]) OVER() FROM ia_input", "42809", "OVER specified, but ia_addon.int_array_enum is not a window function nor an aggregate function", "8", ""},
			{"SELECT ia_addon.int_array_aggregate(DISTINCT v ORDER BY id) FROM ia_input", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "57", ""},
			{"SELECT absent.int_array_aggregate(missing) FROM ia_input", "42703", `column "missing" does not exist`, "35", ""},
			{"SELECT absent.int_array_aggregate(v) FROM ia_input", "3F000", `schema "absent" does not exist`, "8", ""},
			{"SELECT absent.int_array_enum(missing) FROM ia_input", "42703", `column "missing" does not exist`, "30", ""},
			{"SELECT ia_addon.int_array_aggregate(DISTINCT v) OVER() FROM ia_input", "0A000", "DISTINCT is not implemented for window functions", "8", ""},
			{"SELECT ia_addon.int_array_aggregate(v ORDER BY id) OVER() FROM ia_input", "0A000", "aggregate ORDER BY is not implemented for window functions", "8", ""},
			{"SELECT v FROM ia_input WHERE ia_addon.int_array_aggregate(v) IS NULL", "42803", "aggregate functions are not allowed in WHERE", "30", ""},
			{"SELECT ia_addon.int_array_aggregate(v) FROM ia_input GROUP BY ia_addon.int_array_aggregate(v)", "42803", "aggregate functions are not allowed in GROUP BY", "63", ""},
			{"SELECT 'é',ia_addon.int_array_aggregate(v ORDER BY missing) FROM ia_input", "42703", `column "missing" does not exist`, "52", ""},
			{"SELECT 'é',ia_addon.int_array_enum(missing)", "42703", `column "missing" does not exist`, "36", ""},
			{"SELECT absent.int_array_enum(ARRAY[1])", "3F000", `schema "absent" does not exist`, "8", ""},
			{"SELECT ia_addon.int_array_enum(ARRAY[v]) FROM ia_input WHERE ia_addon.int_array_enum(ARRAY[v]) IS NULL", "0A000", "set-returning functions are not allowed in WHERE", "62", ""},
			{"SELECT * FROM ia_addon.int_array_enum('{2}'::integer[]) AS u(v integer)", "42601", `a column definition list is only allowed for functions returning "record"`, "62", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				intaggError(t, p, b, c, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Detail: tc.detail}, false)
			})
		}
		t.Run("missing-window", func(t *testing.T) {
			intaggError(t, p, b, c, "SELECT pg_catalog.int_array_aggregate(v) OVER() FROM ia_input", intaggMissing("pg_catalog.int_array_aggregate(integer)"), false)
		})
		positive := fixture.Select("SELECT ia_addon.int_array_enum(VARIADIC ARRAY[v]) FROM ia_input", []string{"int_array_enum"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {int64(2)}, {int64(-2147483648)}, {int64(2147483647)}})
		positive.Bag = true
		fixture.ArrayAggBoth(run, positive)
		for _, tc := range []struct{ q, arg, code, message, detail string }{
			{"SELECT ia_addon.int_array_aggregate($1)", "bad", "22P02", `invalid input syntax for type integer: "bad"`, ""},
			{"SELECT ia_addon.int_array_aggregate($1)", "2147483648", "22003", `value "2147483648" is out of range for type integer`, ""},
			{"SELECT ia_addon.int_array_enum($1)", "{x}", "22P02", `invalid input syntax for type integer: "x"`, ""},
			{"SELECT ia_addon.int_array_enum($1)", "{1,", "22P02", `malformed array literal: "{1,"`, "Unexpected end of input."},
		} {
			t.Run(tc.q+"-"+tc.arg, func(t *testing.T) {
				w := fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Context: "unnamed portal parameter $1 = '...'"}
				intaggError(t, p, b, c, tc.q, w, true, tc.arg)
			})
		}
		for _, filter := range []string{"", " FILTER(WHERE TRUE)"} {
			q := "SELECT id,ia_addon.int_array_aggregate(v)" + filter + " OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS a FROM ia_input WHERE id<=3 ORDER BY id"
			if b.PG {
				fixture.ArrayAggBoth(run, intaggArray(b, q, []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{2}"}, {int64(2), "{2,NULL}"}, {int64(3), "{2,NULL,2}"}}))
			} else {
				w := fixture.Diagnostic{Code: "0A000", Message: "window function provider is unsupported", Position: fixture.StringAggPosition(q, "ia_addon", false)}
				if filter != "" {
					w.Message = "window aggregate FILTER is unsupported"
					w.Position = fixture.StringAggPosition(q, "FILTER", false)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, w)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		}
	})
}
