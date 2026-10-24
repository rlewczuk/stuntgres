package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesProviders(t *testing.T) {
	// Released pg_proc.dat defaults are immutable/strict; explicit exceptions below.
	for _, tc := range []struct {
		schema, name, inputs, kind, data, udt, lang, source string
		immutable, strict                                   bool
	}{
		{"pg_catalog", "abs", "integer", "FUNCTION", "integer", "int4", "INTERNAL", "int4abs", true, true},
		{"pg_catalog", "row_number", "", "", "bigint", "int8", "INTERNAL", "window_row_number", true, false},
		{"pg_catalog", "count", "", "", "bigint", "int8", "INTERNAL", "aggregate_dummy", true, false},
		{"pg_catalog", "count", `"any"`, "", "bigint", "int8", "INTERNAL", "aggregate_dummy", true, false},
		{"pg_catalog", "lpad", "text,integer", "FUNCTION", "text", "text", "SQL", "", true, true},
		{"pg_catalog", "quote_literal", "anyelement", "FUNCTION", "text", "text", "SQL", "select pg_catalog.quote_literal($1::pg_catalog.text)", false, true},
		{"pg_catalog", "heap_tableam_handler", "internal", "FUNCTION", "table_am_handler", "table_am_handler", "INTERNAL", "heap_tableam_handler", false, true},
		{"pg_catalog", "bthandler", "internal", "FUNCTION", "index_am_handler", "index_am_handler", "INTERNAL", "bthandler", false, true},
		{"pg_catalog", "plpgsql_call_handler", "", "FUNCTION", "language_handler", "language_handler", "C", "plpgsql_call_handler", false, false},
		{"pg_catalog", "plpgsql_inline_handler", "internal", "FUNCTION", "void", "void", "C", "plpgsql_inline_handler", false, true},
		{"pg_catalog", "plpgsql_validator", "oid", "FUNCTION", "void", "void", "C", "plpgsql_validator", false, true},
		{"pg_catalog", "koi8r_to_mic", "integer,integer,cstring,internal,integer,boolean", "FUNCTION", "integer", "int4", "C", "koi8r_to_mic", true, true},
	} {
		t.Run(tc.name+tc.inputs, func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				id := rsuIdentity(t, p, c, tc.schema, tc.name, tc.inputs)
				var external any
				if tc.lang == "C" {
					external = tc.source
				}
				row := irRow(db, tc.schema, tc.name, id, tc.kind, tc.data, "pg_catalog", tc.udt, tc.lang, tc.source, external, tc.immutable, tc.strict)
				irCheck(t, p, b, c, irProjection(b.PG, "specific_schema='"+tc.schema+"' AND specific_name='"+id+"'", [][]any{row}))
			})
		})
	}
	t.Run("generated_and_owned", func(t *testing.T) {
		irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE ir_generated AS RANGE(subtype=integer)", 0))
			run(fixture.Exec("CREATE FUNCTION ir_step(integer,integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+$2'", 0))
			run(fixture.Exec("CREATE AGGREGATE ir_aggregate(integer)(SFUNC=ir_step,STYPE=integer,INITCOND='0')", 0))
			for _, tc := range []struct {
				name, inputs, kind, data, udt, source string
				strict                                bool
			}{
				{"ir_generated", "integer,integer", "FUNCTION", "USER-DEFINED", "ir_generated", "range_constructor2", false},
				{"ir_generated", "integer,integer,text", "FUNCTION", "USER-DEFINED", "ir_generated", "range_constructor3", false},
				{"ir_aggregate", "integer", "", "integer", "int4", "aggregate_dummy", false},
			} {
				id := rsuIdentity(t, p, c, "public", tc.name, tc.inputs)
				schema := "public"
				if tc.udt == "int4" {
					schema = "pg_catalog"
				}
				row := irRow(db, "public", tc.name, id, tc.kind, tc.data, schema, tc.udt, "INTERNAL", tc.source, nil, true, tc.strict)
				irCheck(t, p, b, c, irProjection(b.PG, "specific_schema='public' AND specific_name='"+id+"'", [][]any{row}))
			}
		})
	})
}
