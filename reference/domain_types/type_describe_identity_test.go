package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeTypeof(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, tc := range []struct {
			expression, name string
			oid              int64
		}{
			{"NULL::integer", "integer", 23}, {"NULL::text", "text", 25}, {"NULL::regtype", "regtype", 2206}, {"'abc'::varchar(3)", "character varying", 1043}, {"33", "integer", 23}, {"2147483648", "bigint", 20}, {"1.5", "numeric", 1700}, {"TRUE", "boolean", 16}, {"NULL", "unknown", 705}, {"'x'", "unknown", 705}, {"pg_typeof(33)", "regtype", 2206}, {"'[1,3)'::int4range", "int4range", 3904}, {"NULL::int4range", "int4range", 3904}, {"ARRAY[1,2]", "integer[]", 1007}, {"ROW(1,'x')", "record", 2249},
			{"'x'::unknown", "unknown", 705}, {"NULL::unknown", "unknown", 705}, {"'x'::text", "text", 25},
			// Explicit casts from STRING-category types use release CoerceViaIO.
			{"'x'::text::pg_catalog.unknown", "unknown", 705},
			{"NULL::text::pg_catalog.unknown", "unknown", 705},
			{"'x'::varchar::pg_catalog.unknown", "unknown", 705},
			{"'x'::bpchar::pg_catalog.unknown", "unknown", 705},
			{"'x'::name::pg_catalog.unknown", "unknown", 705},
		} {
			typeDescribeBoth(run, fixture.Select("SELECT pg_typeof("+tc.expression+") AS identity,pg_typeof("+tc.expression+")::oid::bigint AS oid", []string{"identity", "oid"}, []string{"REGTYPE", "INT8"}, [][]any{{tc.name, tc.oid}}))
		}
		for _, target := range []string{`"any"`, "anyelement", "anynonarray", "anycompatible", "anycompatiblenonarray"} {
			for _, literal := range []string{"'x'", "NULL"} {
				typeDescribeBoth(run, fixture.Select("SELECT pg_typeof("+literal+"::"+target+") AS identity,pg_typeof("+literal+"::"+target+")::oid::bigint AS oid", []string{"identity", "oid"}, []string{"REGTYPE", "INT8"}, [][]any{{"unknown", int64(705)}}))
			}
		}
		for _, name := range []string{"unknown", "anyelement"} {
			run(fixture.Exec(`CREATE DOMAIN public."`+name+`" AS text`, 0))
			for _, literal := range []string{"'x'", "NULL"} {
				typeDescribeValue(run, `pg_typeof(`+literal+`::public."`+name+`")::oid='public."`+name+`"'::regtype::oid`, "BOOL", true)
				typeDescribeValue(run, `pg_typeof(`+literal+`::public."`+name+`")::oid<>705::oid`, "BOOL", true)
			}
		}
		run(fixture.Exec("CREATE TABLE public.td_known(v integer)", 0))
		run(fixture.Exec("CREATE DOMAIN public.td_string AS text CONSTRAINT td_string_check CHECK(VALUE<>'bad')", 0))
		run(fixture.Exec("CREATE TABLE public.td_string_inputs(id integer,t text,v varchar(3),b character(3),n name,d public.td_string)", 0))
		run(fixture.Exec("INSERT INTO public.td_string_inputs VALUES(1,'x','x','x','x','x'),(2,NULL,NULL,NULL,NULL,NULL)", 2))
		for _, source := range []string{"t", "v", "b", "n", "d"} {
			typeDescribeBoth(run, fixture.Select("SELECT id,pg_typeof("+source+"::pg_catalog.unknown) AS identity,pg_typeof("+source+"::pg_catalog.unknown)::oid::bigint AS oid FROM public.td_string_inputs ORDER BY id", []string{"id", "identity", "oid"}, []string{"INT4", "REGTYPE", "INT8"}, [][]any{{int64(1), "unknown", int64(705)}, {int64(2), "unknown", int64(705)}}))
		}
		typeDescribeBoth(run, fixture.Select("SELECT id,pg_typeof(t)::oid::bigint AS oid,pg_typeof(d)::oid='public.td_string'::regtype::oid AS nominal FROM public.td_string_inputs ORDER BY id", []string{"id", "oid", "nominal"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(25), true}, {int64(2), int64(25), true}}))
		for _, tc := range []struct {
			query, message string
			position       int
		}{
			{"SELECT pg_typeof(1::pg_catalog.unknown)", "cannot cast type integer to unknown", 19},
			{"SELECT pg_typeof(v::pg_catalog.unknown) FROM public.td_known", "cannot cast type integer to unknown", 19},
			{"SELECT pg_typeof('x'::anyenum)", "cannot cast type unknown to anyenum", 21},
		} {
			t.Run(tc.query, func(t *testing.T) {
				caseRun := func(s fixture.Step) { fixture.RegtypeRunStep(t, p.Ctx, c, b.Recorder, s) }
				d := fixture.ControlsReferenceDiagnostic("42846", tc.message)
				d.Position = fixture.Position(tc.position)
				typeDescribeFailure(caseRun, tc.query, d, true)
			})
		}
		typeDescribeValue(run, "format_type(pg_typeof(1),NULL)", "TEXT", "integer")
		typeDescribeValue(run, "pg_typeof(NULL::integer)=23::oid", "BOOL", true)
		for _, q := range []string{"CREATE TYPE public.td_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=public.td_spans)", "CREATE TYPE public.td_pair AS (v integer,label text)"} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ expression, nominal string }{
			{"public.td_span(1,3)", "public.td_span"}, {"public.td_spans(public.td_span(1,3))", "public.td_spans"}, {"NULL::public.td_span", "public.td_span"}, {"NULL::public.td_spans", "public.td_spans"}, {"ROW(1,'x')::public.td_pair", "public.td_pair"}, {"ARRAY[public.td_span(1,3)]", "public.td_span[]"},
		} {
			typeDescribeValue(run, "pg_typeof("+tc.expression+")::oid='"+tc.nominal+"'::regtype::oid", "BOOL", true)
		}
	})
}

func TestReferenceTypeDescribeEffects(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SEQUENCE public.td_effect START 1", 0))
		q := "SELECT pg_typeof(nextval('public.td_effect')) AS v"
		typeDescribeValue(run, "pg_typeof(nextval('public.td_effect'))", "REGTYPE", "bigint")
		typeDescribeValue(run, "currval('public.td_effect')", "INT8", int64(2))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, n := range []int64{3, 4} {
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"REGTYPE"}, [][]any{{"bigint"}}))
			run(fixture.Select("SELECT currval('public.td_effect') AS v", []string{"v"}, []string{"INT8"}, [][]any{{n}}))
		}
		typeDescribeValue(run, "format_type(NULL::oid,nextval('public.td_effect')::integer)", "TEXT", nil)
		run(fixture.Select("SELECT currval('public.td_effect') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(6)}}))
		for _, q := range []string{"SELECT pg_typeof(1/0)", "SELECT format_type(NULL::oid,1/0)", "SELECT pg_basetype((1/0)::oid::regtype)"} {
			typeDescribeFailure(run, q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false)
		}
		typeDescribeValue(run, "pg_basetype(NULLIF(nextval('public.td_effect'),7)::oid::regtype)", "REGTYPE", nil)
		run(fixture.Select("SELECT currval('public.td_effect') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(8)}}))
		conversion := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		conversion.Context = "unnamed portal parameter $1 = '...'"
		typeDescribeFailure(run, "SELECT pg_typeof($1::integer)", conversion, false, "bad")
		run(fixture.Exec("CREATE DOMAIN public.td_positive AS integer CONSTRAINT td_positive_check CHECK(VALUE>0)", 0))
		typeDescribeValue(run, "pg_typeof(1::public.td_positive)::oid='public.td_positive'::regtype::oid", "BOOL", true)
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain td_positive violates check constraint "td_positive_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "td_positive", "td_positive_check"
		typeDescribeFailure(run, "SELECT pg_typeof((-1)::public.td_positive)", d, false)
		run(fixture.Exec("CREATE SEQUENCE public.td_string_effect START 1", 0))
		stringQuery := "SELECT pg_typeof(nextval('public.td_string_effect')::text::pg_catalog.unknown)::oid::bigint AS oid"
		typeDescribeBoth(run, fixture.Select(stringQuery, []string{"oid"}, []string{"INT8"}, [][]any{{int64(705)}}))
		run(fixture.Select("SELECT currval('public.td_string_effect') AS calls", []string{"calls"}, []string{"INT8"}, [][]any{{int64(2)}}))
		stringStmt, err := c.PrepareContext(p.Ctx, stringQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stringStmt.Close()
		for _, calls := range []int64{3, 4} {
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stringStmt, SQL: stringQuery}, b.Recorder, fixture.Select(stringQuery, []string{"oid"}, []string{"INT8"}, [][]any{{int64(705)}}))
			run(fixture.Select("SELECT currval('public.td_string_effect') AS calls", []string{"calls"}, []string{"INT8"}, [][]any{{calls}}))
		}
		typeDescribeFailure(run, "SELECT pg_typeof((1/0)::text::pg_catalog.unknown)", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false)
		run(fixture.Exec("CREATE DOMAIN public.td_checked_text AS text CONSTRAINT td_checked_text_check CHECK(VALUE<>'bad')", 0))
		stringConstraint := fixture.ControlsReferenceDiagnostic("23514", `value for domain td_checked_text violates check constraint "td_checked_text_check"`)
		stringConstraint.Schema, stringConstraint.DataType, stringConstraint.Constraint = "public", "td_checked_text", "td_checked_text_check"
		typeDescribeFailure(run, "SELECT pg_typeof($1::text::public.td_checked_text::pg_catalog.unknown)", stringConstraint, false, "bad")
	})
}

func TestReferenceTypeDescribeParameters(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		q := "SELECT pg_typeof($1::integer) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []any{int64(7), nil, int64(9)} {
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"REGTYPE"}, [][]any{{"integer"}}, v))
		}
		typeDescribeBoth(run, fixture.Select("SELECT $1::integer AS v,pg_typeof($1) AS t", []string{"v", "t"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(7), "integer"}}, int64(7)))
		typeDescribeBoth(run, fixture.Select("SELECT pg_typeof($1::integer) AS a,pg_typeof($1::text) AS b", []string{"a", "b"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"integer", "text"}}, int64(7)))
		stringQuery := "SELECT pg_typeof($1::text::pg_catalog.unknown) AS identity,pg_typeof($1::text::pg_catalog.unknown)::oid::bigint AS oid,pg_typeof($1::text)::oid::bigint AS concrete"
		stringStmt, err := c.PrepareContext(p.Ctx, stringQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stringStmt.Close()
		for _, value := range []any{"first", nil, "second"} {
			s := fixture.Select(stringQuery, []string{"identity", "oid", "concrete"}, []string{"REGTYPE", "INT8", "INT8"}, [][]any{{"unknown", int64(705), int64(25)}}, value)
			typeDescribeBoth(run, s)
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stringStmt, SQL: stringQuery}, b.Recorder, s)
		}
		for _, tc := range []struct {
			q, state, message string
			position          int
			args              []any
		}{
			{"SELECT pg_typeof($1)", "42P18", "could not determine data type of parameter $1", 0, []any{int64(1)}},
			{"SELECT pg_typeof($1),pg_typeof($1)", "42P18", "could not determine data type of parameter $1", 0, []any{nil}},
			{"SELECT pg_typeof($2::integer)", "42P18", "could not determine data type of parameter $1", 0, []any{nil, int64(1)}},
			{"SELECT pg_typeof($1) AS t,$1::integer AS v", "42P08", "could not determine data type of parameter $1", 18, []any{int64(1)}},
			{"SELECT pg_typeof($1),$1::integer,$1::text", "42P08", "could not determine data type of parameter $1", 18, []any{int64(1)}},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			d.Position = fixture.Position(tc.position)
			typeDescribeFailure(run, tc.q, d, true, tc.args...)
		}
	})
}
