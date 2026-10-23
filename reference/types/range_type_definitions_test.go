package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeDefinitions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "range_type_companions")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE SCHEMA range_type_companions`, 0))
		for i, q := range []string{
			`CREATE TYPE public.alpha_range_tail AS RANGE(SUBTYPE=int4)`,
			`CREATE TYPE public.span AS RANGE(SUBTYPE=integer)`,
			`CREATE TYPE public."UpperRange" AS RANGE(SUBTYPE=integer)`,
			`CREATE TYPE public."Qualified Range" AS RANGE(MULTIRANGE_TYPE_NAME=range_type_companions."Many Spans",SUBTYPE=pg_catalog.int4,SUBTYPE_OPCLASS=pg_catalog.int4_ops)`,
		} {
			s := fixture.Exec(q, 0)
			s.Prepared = i%2 == 1
			run(s)
		}
		run(fixture.Select(`SELECT typname,typtype::text FROM pg_type WHERE typnamespace='public'::regnamespace AND typtype IN ('r','m') ORDER BY typname`, []string{"typname", "typtype"}, []string{"NAME", "TEXT"}, [][]any{{"Qualified Range", "r"}, {"UpperRange", "r"}, {"UpperRange_multirange", "m"}, {"alpha_multirange_tail", "m"}, {"alpha_range_tail", "r"}, {"span", "r"}, {"span_multirange", "m"}}))
		run(fixture.Select(`SELECT typname,typtype::text FROM pg_type WHERE typnamespace='range_type_companions'::regnamespace AND typtype='m'`, []string{"typname", "typtype"}, []string{"NAME", "TEXT"}, [][]any{{"Many Spans", "m"}}))
		run(fixture.Exec(`CREATE TABLE named_ranges(r public."Qualified Range",m range_type_companions."Many Spans")`, 0))
		run(fixture.Prepared(fixture.Exec(`INSERT INTO named_ranges VALUES($1,$2)`, 1, "[1,3]", "{[1,3]}")))
		run(rangeTypeSelect(b, `SELECT r,m FROM named_ranges`, []string{"r", "m"}, []string{"QUALIFIED RANGE", "MANY SPANS"}, [][]any{{"[1,3]", "{[1,3]}"}}, []bool{true, true}))
	})
}

func TestReferenceRangeTypeProviders(t *testing.T) {
	for _, tc := range []struct{ name, setup, definition, input, output string }{
		{"float8", "", `SUBTYPE=float8,SUBTYPE_DIFF=pg_catalog.float8mi`, "[1.5,3.25]", "[1.5,3.25]"},
		{"text", "", `SUBTYPE=text,SUBTYPE_OPCLASS=pg_catalog.text_ops,COLLATION=pg_catalog."C"`, `["a,b","z\\\\q"]`, `["a,b","z\\\\q"]`},
		{"enum", `CREATE TYPE endpoint_label AS ENUM('z','a')`, `SUBTYPE=endpoint_label`, "[z,a]", "[z,a]"},
		{"compatible", "", `SUBTYPE=varchar,SUBTYPE_OPCLASS=pg_catalog.text_ops,COLLATION=pg_catalog."C"`, "[A,a]", "[A,a]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				run(fixture.Exec(`CREATE TYPE provider_span AS RANGE(`+tc.definition+`)`, 0))
				run(fixture.Exec(`CREATE TABLE provider_values(r provider_span)`, 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(`INSERT INTO provider_values VALUES($1)`, 1, tc.input)
					s.Prepared = prepared
					run(s)
				}
				run(rangeTypeSelect(b, `SELECT r FROM provider_values`, []string{"r"}, []string{"PROVIDER_SPAN"}, [][]any{{tc.output}, {tc.output}}, []bool{true}))
				if tc.name == "float8" {
					var id int64
					if err := conn.QueryRowContext(p.Ctx, `SELECT 'pg_catalog.float8mi(double precision,double precision)'::regprocedure::oid::bigint`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select(`SELECT rngsubdiff::oid::bigint FROM pg_range WHERE rngtypid='provider_span'::regtype`, []string{"rngsubdiff"}, []string{"INT8"}, [][]any{{id}}))
				}
				if tc.name == "text" || tc.name == "compatible" {
					var id int64
					if err := conn.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_collation WHERE collnamespace='pg_catalog'::regnamespace AND collname='C'`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select(`SELECT rngcollation::bigint FROM pg_range WHERE rngtypid='provider_span'::regtype`, []string{"rngcollation"}, []string{"INT8"}, [][]any{{id}}))
				}
			})
		})
	}
}

func TestReferenceRangeTypeComparisonConsumption(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(rangeTypeClass("range_subtract_ops", "int4mi"), 0))
		run(fixture.Exec(`CREATE TYPE subtract_span AS RANGE(SUBTYPE=int4,SUBTYPE_OPCLASS=public.range_subtract_ops)`, 0))
		run(fixture.Exec(`CREATE TYPE normal_span AS RANGE(SUBTYPE=int4)`, 0))
		run(fixture.Exec(`CREATE TABLE compare_ranges(k integer,r subtract_span,m subtract_span_multirange)`, 0))
		run(fixture.Exec(`INSERT INTO compare_ranges VALUES(1,'[1,3]','{[1,3]}')`, 1))
		var classID int64
		if err := conn.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_opclass WHERE opcnamespace='public'::regnamespace AND opcname='range_subtract_ops' AND opcmethod=403`).Scan(&classID); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select(`SELECT rngsubopc::bigint FROM pg_range WHERE rngtypid='subtract_span'::regtype`, []string{"rngsubopc"}, []string{"INT8"}, [][]any{{classID}}))
		run(fixture.Exec(`CREATE TABLE normal_ranges(r normal_span)`, 0))
		run(fixture.Exec(`INSERT INTO normal_ranges VALUES('[-2147483648,2147483647]')`, 1))
		run(rangeTypeSelect(b, `SELECT r FROM normal_ranges`, []string{"r"}, []string{"NORMAL_SPAN"}, [][]any{{"[-2147483648,2147483647]"}}, []bool{true}))
		for _, tc := range []struct {
			name, q string
			args    []any
		}{
			{"endpoint", `INSERT INTO compare_ranges VALUES(2,$1,NULL)`, []any{"[-2147483648,2147483647]"}},
			{"sort", `INSERT INTO compare_ranges VALUES(2,NULL,$1)`, []any{"{[-2147483648,-2147483647],[2147483646,2147483647]}"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "22003", Message: "integer out of range", Context: `unnamed portal parameter $1 = '...'`}, tc.args...)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
			})
		}
		run(rangeTypeSelect(b, `SELECT k,r,m FROM compare_ranges ORDER BY k`, []string{"k", "r", "m"}, []string{"INT4", "SUBTRACT_SPAN", "SUBTRACT_SPAN_MULTIRANGE"}, [][]any{{int64(1), "[1,3]", "{[1,3]}"}}, []bool{true, true, true}))
	})
}
