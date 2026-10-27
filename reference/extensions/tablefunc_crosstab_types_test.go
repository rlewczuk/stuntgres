package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabTypes(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
		for _, tc := range []struct {
			source, declaration string
			columns, types      []string
			rows                [][]any
		}{
			{"SELECT 7::integer,'x'::text,11::integer", "k integer,v1 integer,v2 integer", []string{"k", "v1", "v2"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(11), nil}}},
			{"SELECT true,0::integer,42::bigint", "k boolean,v1 bigint,v2 bigint", []string{"k", "v1", "v2"}, []string{"BOOL", "INT8", "INT8"}, [][]any{{true, int64(42), nil}}},
			{"SELECT 'abc'::varchar(3),NULL::boolean,'xy'::varchar(3)", "k varchar(3),v1 varchar(3),v2 varchar(3)", []string{"k", "v1", "v2"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"abc", "xy", nil}}},
			{"SELECT 'abc'::varchar(3),NULL::integer,'xy'::varchar(3)", "k varchar,v1 varchar,v2 varchar", []string{"k", "v1", "v2"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"abc", "xy", nil}}},
			{"SELECT 7,8,9 WHERE false", "k text,v1 text", []string{"k", "v1"}, []string{"TEXT", "TEXT"}, nil},
			{"SELECT 7 WHERE false", "k integer", []string{"k"}, []string{"INT4"}, nil},
		} {
			q := "SELECT * FROM crosstab($1::text) AS t(" + tc.declaration + ")"
			s := fixture.Select(q, tc.columns, tc.types, tc.rows, tc.source)
			run(s)
			run(fixture.Prepared(s))
		}
		// Numeric SQL equality is not the row-name grouping rule: output strings
		// 1.0 and 1.00 remain distinct consecutive groups.
		run(fixture.Exec("CREATE TABLE ct_numeric(seq integer,k numeric,v text)", 0))
		run(fixture.Exec("INSERT INTO ct_numeric VALUES(1,1.0,'first'),(2,1.00,'second'),(3,1.0,'third')", 3))
		numeric := fixture.Select("SELECT k::text AS k,a,b FROM crosstab($1::text) AS t(k numeric,a text,b text)", []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"1.0", "first", nil}, {"1.00", "second", nil}, {"1.0", "third", nil}}, "SELECT k,0,v FROM ct_numeric ORDER BY seq")
		numeric.Bag = true
		run(numeric)
		run(fixture.Prepared(numeric))
		for _, tc := range []struct{ source, declaration, detail string }{
			{"SELECT 'abc'::varchar(3),0,'xy'::varchar(3)", "k varchar(4),v1 varchar", "Source row_name datatype character varying(3) does not match return row_name datatype character varying(4)."},
			{"SELECT 'abc'::varchar(3),0,'xy'::varchar(3)", "k varchar,v1 varchar(4)", "Source value datatype character varying(3) does not match return value datatype character varying(4) in column 2."},
			{"SELECT 7,0,11", "k bigint,v1 integer", "Source row_name datatype integer does not match return row_name datatype bigint."},
		} {
			crosstabFailure(t, p, b, c, "SELECT * FROM crosstab($1::text) AS t("+tc.declaration+")", crosstabReturnError(tc.detail), "execute", tc.source)
		}
		run(fixture.Exec("CREATE DOMAIN ct_int AS integer", 0))
		crosstabFailure(t, p, b, c, "SELECT * FROM crosstab('SELECT 7::ct_int,0,11') AS t(k integer,v1 integer)", crosstabReturnError("Source row_name datatype ct_int does not match return row_name datatype integer."), "execute")
		// Declared dependencies exist even when STRICT suppresses execution.
		run(fixture.Exec("CREATE TYPE ct_result AS (v text)", 0))
		run(fixture.Exec("CREATE VIEW ct_type_view AS SELECT * FROM crosstab(NULL::text) AS t(k text,v ct_result)", 0))
		run(fixture.Select("SELECT * FROM ct_type_view", []string{"k", "v"}, []string{"TEXT", ""}, nil))
	})
}

func TestReferenceTablefuncCrosstabTypeNames(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ct_diag")
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
		run(fixture.Exec("CREATE SCHEMA ct_diag", 0))
		run(fixture.Exec("CREATE DOMAIN ct_diag.ct_int AS integer", 0))
		run(fixture.Exec(`CREATE DOMAIN public."OddType" AS integer`, 0))
		run(fixture.Exec("SET search_path TO public", 0))
		// Domains retain nominal identities in both mismatch directions. The
		// hidden namespace needs qualification; the visible mixed-case name
		// needs quotes. The opposite varchar operand retains its typmod.
		for _, tc := range []struct {
			name, source, declaration, detail string
		}{
			{
				"qualified_source_row",
				"SELECT 7::ct_diag.ct_int,0,'v'::text",
				"k varchar(4),v text",
				"Source row_name datatype ct_diag.ct_int does not match return row_name datatype character varying(4).",
			},
			{
				"qualified_return_row",
				"SELECT 'abc'::varchar(3),0,'v'::text",
				"k ct_diag.ct_int,v text",
				"Source row_name datatype character varying(3) does not match return row_name datatype ct_diag.ct_int.",
			},
			{
				"qualified_source_value",
				"SELECT 1,0,7::ct_diag.ct_int",
				"k integer,v varchar(4)",
				"Source value datatype ct_diag.ct_int does not match return value datatype character varying(4) in column 2.",
			},
			{
				"qualified_return_value",
				"SELECT 1,0,'abc'::varchar(3)",
				"k integer,v ct_diag.ct_int",
				"Source value datatype character varying(3) does not match return value datatype ct_diag.ct_int in column 2.",
			},
			{
				"quoted_source_row",
				`SELECT 7::"OddType",0,'v'::text`,
				"k varchar(4),v text",
				`Source row_name datatype "OddType" does not match return row_name datatype character varying(4).`,
			},
			{
				"quoted_return_row",
				"SELECT 'abc'::varchar(3),0,'v'::text",
				`k "OddType",v text`,
				`Source row_name datatype character varying(3) does not match return row_name datatype "OddType".`,
			},
			{
				"quoted_source_value",
				`SELECT 1,0,7::"OddType"`,
				"k integer,v varchar(4)",
				`Source value datatype "OddType" does not match return value datatype character varying(4) in column 2.`,
			},
			{
				"quoted_return_value",
				"SELECT 1,0,'abc'::varchar(3)",
				`k integer,v "OddType"`,
				`Source value datatype character varying(3) does not match return value datatype "OddType" in column 2.`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				crosstabFailure(t, p, b, c, "SELECT * FROM crosstab($1::text) AS t("+tc.declaration+")", crosstabReturnError(tc.detail), "execute", tc.source)
			})
		}
	})
}

func TestReferenceTablefuncCrosstabTypeArrayNames(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ct_diag")
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
		run(fixture.Exec("CREATE SCHEMA ct_diag", 0))
		for _, ddl := range []string{
			"CREATE DOMAIN public.ct_int AS integer",
			"CREATE DOMAIN ct_diag.ct_int AS integer",
			`CREATE DOMAIN public."OddType" AS integer`,
			`CREATE DOMAIN ct_diag."OddType" AS integer`,
		} {
			run(fixture.Exec(ddl, 0))
		}
		run(fixture.Exec("SET search_path TO public", 0))
		// Array diagnostics follow element visibility and identifier quoting,
		// not the element's stored, always-qualified declaration spelling.
		for _, typ := range []struct {
			name, annotation, display string
		}{
			{"visible", "public.ct_int[]", "ct_int[]"},
			{"hidden", "ct_diag.ct_int[]", "ct_diag.ct_int[]"},
			{"visible_quoted", `public."OddType"[]`, `"OddType"[]`},
			{"hidden_quoted", `ct_diag."OddType"[]`, `ct_diag."OddType"[]`},
		} {
			t.Run(typ.name, func(t *testing.T) {
				for _, tc := range []struct {
					name, source, declaration, detail string
				}{
					{
						"source_row",
						"SELECT '{7}'::" + typ.annotation + ",0,'v'::text",
						"k varchar(4),v text",
						"Source row_name datatype " + typ.display + " does not match return row_name datatype character varying(4).",
					},
					{
						"return_row",
						"SELECT 'abc'::varchar(3),0,'v'::text",
						"k " + typ.annotation + ",v text",
						"Source row_name datatype character varying(3) does not match return row_name datatype " + typ.display + ".",
					},
					{
						"source_value",
						"SELECT 1,0,'{7}'::" + typ.annotation,
						"k integer,v varchar(4)",
						"Source value datatype " + typ.display + " does not match return value datatype character varying(4) in column 2.",
					},
					{
						"return_value",
						"SELECT 1,0,'abc'::varchar(3)",
						"k integer,v " + typ.annotation,
						"Source value datatype character varying(3) does not match return value datatype " + typ.display + " in column 2.",
					},
				} {
					t.Run(tc.name, func(t *testing.T) {
						crosstabFailure(t, p, b, c, "SELECT * FROM crosstab($1::text) AS t("+tc.declaration+")", crosstabReturnError(tc.detail), "execute", tc.source)
					})
				}
			})
		}
	})
}
