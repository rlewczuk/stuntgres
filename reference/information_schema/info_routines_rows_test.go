package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesRows(t *testing.T) {
	cases := []struct {
		name, args, inputs, result, data, udt, language, source string
		procedure, standard                                     bool
	}{
		{"ir_over", "integer", "integer", "integer", "integer", "int4", "SQL", " SELECT $1 + 1 ", false, false},
		{"ir_over", "text", "text", "text", "text", "text", "SQL", " SELECT $1 ", false, false},
		{"ir_zero", "", "", "integer", "integer", "int4", "SQL", " SELECT 7 ", false, false},
		{"ir_named", "x integer DEFAULT 4", "integer", "integer", "integer", "int4", "SQL", " SELECT x ", false, false},
		{"ir_inout", "INOUT x integer", "integer", "", "integer", "int4", "SQL", " SELECT x ", false, false},
		{"ir_out", "OUT x integer", "", "", "integer", "int4", "SQL", " SELECT 2 ", false, false},
		{"ir_variadic", "VARIADIC x integer[]", "integer[]", "integer", "integer", "int4", "SQL", " SELECT 1 ", false, false},
		{"ir_standard", "", "", "integer", "integer", "int4", "SQL", "", false, true},
		{"ir_pl", "", "", "integer", "integer", "int4", "plpgsql", "BEGIN RETURN 9; END", false, false},
		{"ir_proc", "", "", "", "", "", "SQL", " SELECT 1 ", true, false},
		{"ir_plproc", "", "", "", "", "", "plpgsql", "BEGIN RETURN; END", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.inputs, func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				kind, ddl := "FUNCTION", "CREATE FUNCTION "
				if tc.procedure {
					kind, ddl = "PROCEDURE", "CREATE PROCEDURE "
				}
				q := ddl + tc.name + "(" + tc.args + ")"
				if tc.result != "" {
					q += " RETURNS " + tc.result
				}
				if tc.standard {
					q += " RETURN 1"
				} else {
					q += " LANGUAGE " + tc.language + " AS $$" + tc.source + "$$"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				id := rsuIdentity(t, p, c, "public", tc.name, tc.inputs)
				lang := tc.language
				if lang == "plpgsql" {
					lang = "PLPGSQL"
				}
				row := irRow(db, "public", tc.name, id, kind, tc.data, "pg_catalog", tc.udt, lang, tc.source, nil, false, false)
				irCheck(t, p, b, c, irProjection(b.PG, "routine_schema='public' AND routine_name='"+tc.name+"'", [][]any{row}))
			})
		})
	}
	t.Run("overload_bag_and_no_execution", func(t *testing.T) {
		irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE ir_side_effect", 0))
			run(fixture.Exec("CREATE FUNCTION ir_over(integer) RETURNS bigint LANGUAGE SQL AS $$ SELECT nextval('ir_side_effect') $$", 0))
			run(fixture.Exec("CREATE FUNCTION ir_over(text) RETURNS text LANGUAGE SQL AS $$ SELECT $1 $$", 0))
			a := irRow(db, "public", "ir_over", rsuIdentity(t, p, c, "public", "ir_over", "integer"), "FUNCTION", "bigint", "pg_catalog", "int8", "SQL", " SELECT nextval('ir_side_effect') ", nil, false, false)
			z := irRow(db, "public", "ir_over", rsuIdentity(t, p, c, "public", "ir_over", "text"), "FUNCTION", "text", "pg_catalog", "text", "SQL", " SELECT $1 ", nil, false, false)
			if a[2] == z[2] {
				t.Fatal("overloads share specific identity")
			}
			irCheck(t, p, b, c, irProjection(b.PG, "routine_schema='public' AND routine_name='ir_over'", [][]any{a, z}))
			run(fixture.Select("SELECT last_value,is_called FROM ir_side_effect", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	})
}

func TestReferenceInfoRoutinesReturns(t *testing.T) {
	for _, tc := range []struct{ name, setup, result, data, schema, udt, expression string }{
		{"integer", "", "integer", "integer", "pg_catalog", "int4", "NULL::integer"},
		{"text", "", "text", "text", "pg_catalog", "text", "NULL::text"},
		{"varchar", "", "varchar(7)", "character varying", "pg_catalog", "varchar", "NULL::varchar(7)"},
		{"numeric", "", "numeric(8,2)", "numeric", "pg_catalog", "numeric", "NULL::numeric(8,2)"},
		{"time", "", "timestamptz(2)", "timestamp with time zone", "pg_catalog", "timestamptz", "NULL::timestamptz(2)"},
		{"array", "", "integer[]", "ARRAY", "pg_catalog", "_int4", "NULL::integer[]"},
		{"enum", "CREATE TYPE ir_enum AS ENUM('x')", "ir_enum", "USER-DEFINED", "public", "ir_enum", "NULL::ir_enum"},
		{"domain", "CREATE DOMAIN ir_domain AS integer", "ir_domain", "USER-DEFINED", "public", "ir_domain", "NULL::ir_domain"},
		{"domain_array", "CREATE DOMAIN ir_domain_array AS integer[]", "ir_domain_array", "USER-DEFINED", "public", "ir_domain_array", "NULL::ir_domain_array"},
		{"array_domain", "CREATE DOMAIN ir_element AS integer", "ir_element[]", "ARRAY", "public", "_ir_element", "NULL::ir_element[]"},
		{"range", "CREATE TYPE ir_range AS RANGE(subtype=integer)", "ir_range", "USER-DEFINED", "public", "ir_range", "NULL::ir_range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				name := "ir_return_" + tc.name
				source := " SELECT " + tc.expression + " "
				run(fixture.Exec("CREATE FUNCTION "+name+"() RETURNS "+tc.result+" LANGUAGE SQL AS $$"+source+"$$", 0))
				row := irRow(db, "public", name, rsuIdentity(t, p, c, "public", name, ""), "FUNCTION", tc.data, tc.schema, tc.udt, "SQL", source, nil, false, false)
				irCheck(t, p, b, c, irProjection(b.PG, "routine_schema='public' AND routine_name='"+name+"'", [][]any{row}))
			})
		})
	}
	// Genuine existing descriptors exercise nominal SRF/polymorphic/void results
	// without requiring unrelated user CREATE FUNCTION capabilities.
	for _, tc := range []struct {
		name, inputs, data, udt, source string
		immutable                       bool
	}{
		{"generate_series", "integer,integer", "integer", "int4", "generate_series_int4", true},
		{"unnest", "anyarray", "anyelement", "anyelement", "array_unnest", true},
		{"pg_sleep", "double precision", "void", "void", "pg_sleep", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				id := rsuIdentity(t, p, c, "pg_catalog", tc.name, tc.inputs)
				row := irRow(db, "pg_catalog", tc.name, id, "FUNCTION", tc.data, "pg_catalog", tc.udt, "INTERNAL", tc.source, nil, tc.immutable, true)
				irCheck(t, p, b, c, irProjection(b.PG, "specific_schema='pg_catalog' AND specific_name='"+id+"'", [][]any{row}))
			})
		})
	}
}
