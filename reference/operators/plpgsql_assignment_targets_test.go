package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentCoercion(t *testing.T) {
	t.Run("selected_cast", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE pa_source AS ENUM ('7')",
				"CREATE FUNCTION pa_cast(pa_source) RETURNS integer LANGUAGE SQL AS 'SELECT 99'")
			// Explicit cleanup precedes creation even though this pair also depends
			// on a scenario-local type. Never override a released builtin pair.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "DROP CAST IF EXISTS (pa_source AS integer)"); err != nil {
					t.Error(err)
				}
			}()
			userOperatorExec(t, p, b, c,
				"CREATE CAST (pa_source AS integer) WITH FUNCTION pa_cast(pa_source) AS ASSIGNMENT",
				"CREATE FUNCTION pa_scalar() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN n := '7'::pa_source; RETURN n; END$$",
				"CREATE FUNCTION pa_leaf() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := '{1,2}'; BEGIN a[1] = '7'::pa_source; RETURN a; END$$")
			plpgsqlValue(t, p, b, c, "SELECT pa_scalar() AS v", "INT4", int64(99))
			plpgsqlValue(t, p, b, c, "SELECT pa_leaf()::text AS v", "TEXT", "{99,2}")
		})
	})
	for _, tc := range []struct{ name, body, want string }{
		{"text_element", "DECLARE a integer[] := '{1,2}'; BEGIN a[2] := '23'::text; RETURN a; END", "{1,23}"},
		{"assignment_array_cast", "DECLARE a bigint[] := '{1,2}'; BEGIN a[1:2] = ARRAY[3,4]::integer[]; RETURN a; END", "{3,4}"},
		{"null_element", "DECLARE a integer[] := '{1,2}'; BEGIN a[1] = NULL; RETURN a; END", "{NULL,2}"},
		{"varchar_element", "DECLARE a varchar(3)[] := '{one,two}'; BEGIN a[2] := 'yes'::text; RETURN a; END", "{one,yes}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ret := "integer[]"
				if tc.name == "assignment_array_cast" {
					ret = "bigint[]"
				}
				if tc.name == "varchar_element" {
					ret = "varchar[]"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pa_value() RETURNS "+ret+" LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pa_value()::text AS v", "TEXT", tc.want)
			})
		})
	}
	t.Run("domain_containers_and_source_retention", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE DOMAIN pa_positive AS integer CHECK(VALUE>0)",
				"CREATE DOMAIN pa_array AS integer[] CHECK(VALUE[1]>0)",
				"CREATE TYPE pa_box AS (a pa_positive[], sibling integer)",
				"CREATE DOMAIN pa_box_domain AS pa_box CHECK((VALUE).sibling=9)",
				"CREATE FUNCTION pa_domains(x integer[]) RETURNS text LANGUAGE plpgsql AS $$DECLARE a pa_array := x; r pa_box_domain := ROW(x,9); BEGIN a[1] := '7'::text; r.a[2] = '8'::text; RETURN a::text || '/' || r.a::text || '/' || x::text; END$$")
			plpgsqlValue(t, p, b, c, "SELECT pa_domains('{1,2}') AS v", "TEXT", "{7,2}/{1,8}/{1,2}")
		})
	})
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"composite_text_element", "DECLARE a pa_to[] := ARRAY[NULL::pa_to]; BEGIN a[1] := '(7)'::text; RETURN (a[1]).n; END", 7},
		{"composite_text_field", "DECLARE r record; BEGIN r := ROW(NULL::pa_to); r.f1 := '(7)'::text; RETURN (r.f1).n; END", 7},
		{"composite_text_whole", "DECLARE r pa_to; BEGIN r := '(8)'::text; RETURN r.n; END", 8},
		{"composite_ordinal_whole", "DECLARE x pa_from := ROW(7); r pa_to; BEGIN r := x; RETURN r.n; END", 7},
		{"composite_record_adopts", "DECLARE x pa_from := ROW(7); r record; BEGIN r := x; RETURN r.n; END", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TYPE pa_from AS (n integer)",
					"CREATE TYPE pa_to AS (n integer)",
					"CREATE FUNCTION pa_composite() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pa_composite() AS v", "INT4", tc.want)
			})
		})
	}
	t.Run("composite_errors", plpgsqlCompositeAssignmentErrors)
}

func TestReferencePLpgSQLAssignmentTargets(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"record_array", "DECLARE r record; BEGIN r := ROW('{1,2}'::integer[]); r.f1[2] = '23'::text; RETURN r.f1::text; END", "{1,23}"},
		{"composite_array", "DECLARE a pa_pair[] := ARRAY[ROW(1,'keep')::pa_pair,ROW(2,'other')::pa_pair]; BEGIN a[1].n := 7; RETURN a::text; END", "{\"(7,keep)\",\"(2,other)\"}"},
		{"null_composite_element", "DECLARE a pa_pair[] := ARRAY[NULL::pa_pair]; BEGIN a[1].n = 7; RETURN a::text; END", "{\"(7,)\"}"},
		{"row_array", "DECLARE r pa_box := ROW('{1,2}'::integer[],9); BEGIN r.a[2] := 7; RETURN r::text; END", "(\"{1,7}\",9)"},
		{"null_row", "DECLARE r pa_box; BEGIN r.a[3] := 7; RETURN r.a::text; END", "[3:3]={7}"},
		{"alias", "DECLARE a integer[] := '{1,2}'; x ALIAS FOR a; BEGIN x[1] = 7; RETURN a::text; END", "{7,2}"},
		{"label", "<<outer_block>> DECLARE a integer[] := '{1,2}'; BEGIN outer_block.a[2] := 7; RETURN a::text; END", "{1,7}"},
		{"nested_path", "DECLARE a pa_box[] := ARRAY[ROW('{1,2}'::integer[],9)::pa_box]; BEGIN a[1].a[2] = 7; RETURN a[1].a::text; END", "{1,7}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TYPE pa_pair AS (n integer, label text)",
					"CREATE TYPE pa_box AS (a integer[], sibling integer)",
					"CREATE FUNCTION pa_target() RETURNS text LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pa_target() AS v", "TEXT", tc.want)
			})
		})
	}
	t.Run("formal_roots_and_caller_retention", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION pa_formal(a integer[]) RETURNS integer[] LANGUAGE plpgsql AS $$BEGIN $1[1] := 7; a[2] = 8; RETURN a; END$$",
				"CREATE TABLE pa_inputs(a integer[])")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pa_inputs VALUES ('{1,2}')", 1))
			plpgsqlValue(t, p, b, c, "SELECT pa_formal(a)::text AS v FROM pa_inputs", "TEXT", "{7,8}")
			plpgsqlValue(t, p, b, c, "SELECT a::text AS v FROM pa_inputs", "TEXT", "{1,2}")
		})
	})
}
