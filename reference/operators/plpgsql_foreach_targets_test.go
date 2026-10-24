package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachTargets(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, target, slice, source, expression, want string
	}{
		{"bigint", "x bigint", "x", "", "'{1,2}'::integer[]", "x::text", "12"},
		{"text", "x text", "x", "", "'{1,2}'::integer[]", "x", "12"},
		{"text_input", "x integer", "x", "", "'{12,34}'::text[]", "x::text", "1234"},
		{"empty_no_conversion", "x integer:=99", "x", "", "'{}'::text[]", "x::text", ""},
		{"bigint_slice", "x bigint[]", "x", " SLICE 1", "'{{1,2},{3,4}}'::integer[]", "x::text", "{1,2}{3,4}"},
		{"text_slice", "x text[]", "x", " SLICE 1", "'{{1,2},{3,4}}'::integer[]", "x::text", "{1,2}{3,4}"},
		{"alias", "x ALIAS FOR $1", "x", "", "'{1,2}'::integer[]", "x::text", "12"},
		{"formal", "unused integer", "arg", "", "'{1,2}'::integer[]", "arg::text", "12"},
		{"qualified", "x integer", "outer_block.x", "", "'{1,2}'::integer[]", "outer_block.x::text", "12"},
		{"field", "r fe_pair", "r.a", "", "'{1,2}'::integer[]", "r.a::text", "12"},
		{"list", "x integer; y text", "x,y", "", "'{\"(1,a)\",\"(2,b)\"}'::fe_pair[]", "x::text||y", "1a2b"},
		{"row", "r fe_pair", "r", "", "'{\"(1,a)\",\"(2,b)\"}'::fe_pair[]", "r.a::text||r.b", "1a2b"},
		{"record", "r record", "r", "", "'{\"(1,a)\",\"(2,b)\"}'::fe_pair[]", "r.a::text||r.b", "1a2b"},
		{"null_row", "r fe_pair", "r", "", "'{\"(1,a)\",NULL,\"(2,b)\"}'::fe_pair[]", "coalesce(r.a::text,'N')||coalesce(r.b,'N')", "1aNN2b"},
		{"null_record", "r record", "r", "", "'{\"(1,a)\",NULL,\"(2,b)\"}'::fe_pair[]", "coalesce(r.a::text,'N')||coalesce(r.b,'N')", "1aNN2b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TYPE fe_pair AS (a integer,b text)",
					"CREATE FUNCTION fe_target(arg integer) RETURNS text LANGUAGE plpgsql AS $$<<outer_block>> DECLARE "+tc.declaration+"; s text:=''; BEGIN FOREACH "+tc.target+tc.slice+" IN ARRAY "+tc.source+" LOOP s:=s||("+tc.expression+"); END LOOP; RETURN s; END$$")
				if tc.name == "null_record" {
					want := plpgsqlRuntimeDiagnostic("55000", `record "r" is not assigned yet`, "fe_target(integer)", "assignment")
					want.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
					plpgsqlError(t, p, c, "SELECT fe_target(99) AS v", true, want)
				} else {
					plpgsqlValue(t, p, b, c, "SELECT fe_target(99) AS v", "TEXT", tc.want)
					foreachHeld(t, p, b, c, "SELECT fe_target($1::integer) AS v", "TEXT", [][]any{{int64(99)}, {int64(0)}}, []any{tc.want, tc.want})
				}
			})
		})
	}
}

// The pinned oracle resolves domain identity, row shape and sequential
// publication independently of native behavior.
func TestReferencePLpgSQLForeachProbes(t *testing.T) {
	for _, tc := range []struct {
		name, setup, declaration, target, slice, source, body, result, want string
	}{
		{"domain_source", "CREATE DOMAIN fe_whole AS integer[]", "x integer", "x", "", "'{1,2}'::fe_whole", "n:=n+1;", "n::text", ""},
		{"domain_target", "CREATE DOMAIN fe_whole AS integer[]", "x fe_whole", "x", " SLICE 1", "'{{1,2},{3,4}}'::integer[]", "n:=n+1;", "n::text", ""},
		{"element_domain", "CREATE DOMAIN fe_elem AS integer", "x integer", "x", "", "'{1,2}'::fe_elem[]", "n:=n+x;", "n::text", "3"},
		{"array_domain_element", "CREATE DOMAIN fe_whole AS integer[]", "x fe_whole", "x", "", "'{\"{1,2}\",\"{3,4}\"}'::fe_whole[]", "n:=n+1;", "n::text", "2"},
		{"width_shortage", "CREATE TYPE fe_pair AS (a integer,b text)", "x integer; y text; z integer:=99", "x,y,z", "", "'{\"(1,a)\"}'::fe_pair[]", "s:=coalesce(x::text,'N')||coalesce(y,'N')||coalesce(z::text,'N');", "s", "1aN"},
		{"width_excess", "CREATE TYPE fe_pair AS (a integer,b text)", "x integer", "x", "", "'{\"(1,a)\"}'::fe_pair[]", "s:=x::text;", "s", ""},
		{"duplicate_target", "CREATE TYPE fe_pair AS (a integer,b text)", "x text", "x,x", "", "'{\"(1,a)\",\"(2,b)\"}'::fe_pair[]", "s:=s||x;", "s", "ab"},
		{"repeated_root", "CREATE TYPE fe_pair AS (a integer,b text)", "r fe_pair", "r.a,r.b", "", "'{\"(1,a)\",\"(2,b)\"}'::fe_pair[]", "s:=s||r.a::text||r.b;", "s", "1a2b"},
		{"partial_list_failure", "CREATE TYPE fe_pair AS (a integer,b text)", "x integer:=90; y integer:=91", "x,y", "", "'{\"(1,bad)\"}'::fe_pair[]", "", "coalesce(x::text,'N')||':'||coalesce(y::text,'N')", "1:91"},
		{"partial_root_failure", "CREATE TYPE fe_pair AS (a integer,b text)", "r fe_pair := '(90,91)'::fe_pair", "r.b,r.a", "", "'{\"(1,bad)\"}'::fe_pair[]", "", "r.a::text||':'||r.b", "90:1"},
		{"null_record_shape", "CREATE TYPE fe_pair AS (a integer,b text)", "r record", "r", "", "'{\"(1,a)\",NULL,\"(2,b)\"}'::fe_pair[]", "s:=s||coalesce(r.a::text,'N')||coalesce(r.b,'N')||':'||(r IS NULL)::text||';';", "s", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, tc.setup)
				loop := "FOREACH " + tc.target + tc.slice + " IN ARRAY " + tc.source + " LOOP " + tc.body + " END LOOP;"
				if tc.name == "partial_list_failure" || tc.name == "partial_root_failure" {
					loop = "BEGIN " + loop + " EXCEPTION WHEN invalid_text_representation THEN END;"
				}
				q := "CREATE FUNCTION fe_probe() RETURNS text LANGUAGE plpgsql AS $$DECLARE " + tc.declaration + "; n integer:=0; s text:=''; BEGIN " + loop + " RETURN " + tc.result + "; END$$"
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					var want fixture.Diagnostic
					switch tc.name {
					case "domain_source":
						want = plpgsqlRuntimeDiagnostic("42804", "FOREACH expression must yield an array, not type fe_whole", "fe_probe()", "FOREACH over array")
					case "domain_target":
						want = plpgsqlRuntimeDiagnostic("42804", "FOREACH ... SLICE loop variable must be of an array type", "fe_probe()", "FOREACH over array")
					case "width_excess":
						want = plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "(1,a)"`, "fe_probe()", "FOREACH over array")
					case "null_record_shape":
						want = plpgsqlRuntimeDiagnostic("55000", `record "r" is not assigned yet`, "fe_probe()", "assignment")
						want.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
					}
					if want.Code != "" {
						plpgsqlError(t, p, c, "SELECT fe_probe() AS v", true, want)
					} else {
						plpgsqlValue(t, p, b, c, "SELECT fe_probe() AS v", "TEXT", tc.want)
						foreachHeld(t, p, b, c, "SELECT fe_probe() AS v", "TEXT", [][]any{nil, nil}, []any{tc.want, tc.want})
					}
					userOperatorExec(t, p, b, c, "DROP FUNCTION fe_probe()")
				}
			})
		})
	}
}
