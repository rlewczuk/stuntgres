package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func typeDescribeConsumersSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE public.td_consumers(id integer,v integer,label text DEFAULT format_type(23,NULL),identity regtype DEFAULT pg_typeof(NULL::integer),CONSTRAINT td_int CHECK(pg_typeof(v)::oid=23::oid))",
		"CREATE VIEW public.td_consumer_view AS SELECT id,format_type(pg_basetype(pg_typeof(v)),NULL) AS label,pg_typeof(v) AS identity,pg_typeof('x') AS unknown_identity FROM public.td_consumers",
		"CREATE VIEW public.td_unknown_view AS SELECT pg_typeof('x'::pg_catalog.unknown) AS explicit_string,pg_typeof(NULL::pg_catalog.unknown) AS explicit_null,pg_typeof('x'::text) AS concrete,pg_typeof('x'::anyelement) AS pseudo,pg_typeof(NULL::anynonarray) AS pseudo_null",
		`CREATE VIEW public.td_pseudo_view AS SELECT pg_typeof('x'::"any") AS any_string,pg_typeof(NULL::"any") AS any_null,pg_typeof('x'::anyelement) AS element_string,pg_typeof(NULL::anyelement) AS element_null,pg_typeof('x'::anynonarray) AS nonarray_string,pg_typeof(NULL::anynonarray) AS nonarray_null,pg_typeof('x'::anycompatible) AS compatible_string,pg_typeof(NULL::anycompatible) AS compatible_null,pg_typeof('x'::anycompatiblenonarray) AS compatible_nonarray_string,pg_typeof(NULL::anycompatiblenonarray) AS compatible_nonarray_null`,
		"CREATE DOMAIN public.td_consumer_string AS text CONSTRAINT td_consumer_string_check CHECK(VALUE<>'bad')",
		"CREATE TABLE public.td_unknown_inputs(id integer,t text,v varchar(3),d public.td_consumer_string)",
		"CREATE VIEW public.td_unknown_sources AS SELECT id,pg_typeof(t::pg_catalog.unknown) AS textual,pg_typeof(v::pg_catalog.unknown) AS varying,pg_typeof(d::pg_catalog.unknown) AS nominal,pg_typeof(t) AS concrete FROM public.td_unknown_inputs",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO public.td_consumers(id,v) VALUES(1,7),(2,NULL)", 2))
	run(fixture.Exec("INSERT INTO public.td_unknown_inputs VALUES(1,'x','x','x'),(2,NULL,NULL,NULL)", 2))
}

func typeDescribeConsumersProof(run func(fixture.Step)) {
	typeDescribeBoth(run, fixture.Select("SELECT id,v,label,identity FROM public.td_consumers ORDER BY id", []string{"id", "v", "label", "identity"}, []string{"INT4", "INT4", "TEXT", "REGTYPE"}, [][]any{{int64(1), int64(7), "integer", "integer"}, {int64(2), nil, "integer", "integer"}}))
	typeDescribeBoth(run, fixture.Select("SELECT id,label,identity,unknown_identity FROM public.td_consumer_view ORDER BY id", []string{"id", "label", "identity", "unknown_identity"}, []string{"INT4", "TEXT", "REGTYPE", "REGTYPE"}, [][]any{{int64(1), "integer", "integer", "unknown"}, {int64(2), "integer", "integer", "unknown"}}))
	typeDescribeUnknownViewProof(run, "public.td_unknown_view", "unknown")
	typeDescribePseudoViewProof(run, "public.td_pseudo_view", "unknown")
	typeDescribeUnknownSourcesProof(run, "public.td_unknown_sources", "unknown")
}

func TestReferenceTypeDescribeConsumers(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeConsumersSetup(run)
		typeDescribeConsumersProof(run)
		// A domain whose name resembles a pseudo type is not builtin authority.
		run(fixture.Exec(`CREATE DOMAIN public."unknown" AS text`, 0))
		run(fixture.Exec("SET search_path TO public,pg_catalog", 0))
		typeDescribeFreezeUnknown(t, p, c, run, "pg_catalog.unknown")
		typeDescribeValue(run, `pg_typeof('x'::public."unknown")::oid='public."unknown"'::regtype::oid`, "BOOL", true)
		run(fixture.Exec("SET search_path TO public", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("INSERT INTO public.td_consumers(id,v) VALUES($1,$2) RETURNING pg_typeof(v) AS identity,format_type(pg_typeof(v),NULL) AS label", []string{"identity", "label"}, []string{"REGTYPE", "TEXT"}, [][]any{{"integer", "integer"}}, int64(3), nil)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("DELETE FROM public.td_consumers WHERE id=3", 1))
		}
		run(fixture.Exec("CREATE TABLE public.td_default_parent(label text PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE public.td_default_child(label text DEFAULT format_type(23,NULL) REFERENCES public.td_default_parent(label) ON DELETE SET DEFAULT)", 0))
		run(fixture.Exec("INSERT INTO public.td_default_parent VALUES('old'),('integer')", 2))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("INSERT INTO public.td_default_child VALUES('old')", 1))
			deletion := fixture.Exec("DELETE FROM public.td_default_parent WHERE label='old'", 1)
			deletion.Prepared = prepared
			run(deletion)
			typeDescribeBoth(run, fixture.Select("SELECT label FROM public.td_default_child", []string{"label"}, []string{"TEXT"}, [][]any{{"integer"}}))
			run(fixture.Exec("DELETE FROM public.td_default_child", 1))
			run(fixture.Exec("INSERT INTO public.td_default_parent VALUES('old')", 1))
		}
		run(fixture.Exec("CREATE TABLE public.td_atomic(id integer,label text)", 0))
		typeDescribeFailure(run, "INSERT INTO public.td_atomic VALUES(1,format_type(23,NULL)),(2,format_type(1186,0)) RETURNING id", fixture.ControlsReferenceDiagnostic("XX000", "invalid INTERVAL typmod: 0x0"), false)
		run(fixture.Select("SELECT id,label FROM public.td_atomic ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, nil))
		d := fixture.ControlsReferenceDiagnostic("42883", "function pg_typeof() does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "8"
		typeDescribeFailure(run, "SELECT pg_typeof() FROM public.td_consumers WHERE id=99", d, true)
	})
}

func typeDescribeUnknownViewProof(run func(fixture.Step), relation, unknownName string) {
	typeDescribeBoth(run, fixture.Select("SELECT explicit_string,explicit_null,concrete,pseudo,pseudo_null FROM "+relation, []string{"explicit_string", "explicit_null", "concrete", "pseudo", "pseudo_null"}, []string{"REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{unknownName, unknownName, "text", unknownName, unknownName}}))
	typeDescribeBoth(run, fixture.Select("SELECT explicit_string::oid::bigint AS a,explicit_null::oid::bigint AS b,concrete::oid::bigint AS c,pseudo::oid::bigint AS d,pseudo_null::oid::bigint AS e FROM "+relation, []string{"a", "b", "c", "d", "e"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(705), int64(705), int64(25), int64(705), int64(705)}}))
}

func typeDescribeFreezeUnknown(t *testing.T, p *fixture.Pair, c *sql.Conn, run func(fixture.Step), unknownName string) {
	t.Helper()
	for _, names := range []struct{ source, target string }{
		{"public.td_unknown_view", "public.td_unknown_frozen"},
		{"public.td_unknown_sources", "public.td_unknown_sources_frozen"},
		{"public.td_pseudo_view", "public.td_pseudo_frozen"},
	} {
		var frozen string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass,true)", names.source).Scan(&frozen); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE VIEW "+names.target+" AS "+frozen, 0))
	}
	typeDescribeUnknownViewProof(run, "public.td_unknown_frozen", unknownName)
	typeDescribeUnknownSourcesProof(run, "public.td_unknown_sources_frozen", unknownName)
	typeDescribePseudoViewProof(run, "public.td_pseudo_frozen", unknownName)
}

func typeDescribeUnknownSourcesProof(run func(fixture.Step), relation, unknownName string) {
	typeDescribeBoth(run, fixture.Select("SELECT id,textual,varying,nominal,concrete FROM "+relation+" ORDER BY id", []string{"id", "textual", "varying", "nominal", "concrete"}, []string{"INT4", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{int64(1), unknownName, unknownName, unknownName, "text"}, {int64(2), unknownName, unknownName, unknownName, "text"}}))
	typeDescribeBoth(run, fixture.Select("SELECT id,textual::oid::bigint AS a,varying::oid::bigint AS b,nominal::oid::bigint AS c,concrete::oid::bigint AS d FROM "+relation+" ORDER BY id", []string{"id", "a", "b", "c", "d"}, []string{"INT4", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(705), int64(705), int64(705), int64(25)}, {int64(2), int64(705), int64(705), int64(705), int64(25)}}))
}

func typeDescribePseudoViewProof(run func(fixture.Step), relation, unknownName string) {
	columns := []string{"any_string", "any_null", "element_string", "element_null", "nonarray_string", "nonarray_null", "compatible_string", "compatible_null", "compatible_nonarray_string", "compatible_nonarray_null"}
	types := []string{"REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE"}
	typeDescribeBoth(run, fixture.Select("SELECT * FROM "+relation, columns, types, [][]any{{unknownName, unknownName, unknownName, unknownName, unknownName, unknownName, unknownName, unknownName, unknownName, unknownName}}))
	typeDescribeBoth(run, fixture.Select("SELECT any_string::oid=705::oid AND any_null::oid=705::oid AND element_string::oid=705::oid AND element_null::oid=705::oid AND nonarray_string::oid=705::oid AND nonarray_null::oid=705::oid AND compatible_string::oid=705::oid AND compatible_null::oid=705::oid AND compatible_nonarray_string::oid=705::oid AND compatible_nonarray_null::oid=705::oid AS logical FROM "+relation, []string{"logical"}, []string{"BOOL"}, [][]any{{true}}))
}
