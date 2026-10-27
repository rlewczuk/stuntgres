package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeVisibility(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE SCHEMA td", "CREATE SCHEMA td_hidden",
			`CREATE DOMAIN td."Outer Mixed" AS integer`, `CREATE DOMAIN td."a""b" AS text`,
			"CREATE DOMAIN td.names AS name[]", "CREATE DOMAIN td_hidden.names AS integer",
			"CREATE TYPE td.mood AS ENUM ('calm','busy')", "CREATE TYPE td.pair AS (v integer)",
			"CREATE TYPE td.span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=td.spans)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, path := range []struct{ set, prefix string }{{"public", "td."}, {"td,public", ""}, {"td_hidden,td,public", ""}} {
			run(fixture.Exec("SET search_path TO "+path.set, 0))
			for _, tc := range []struct{ name, formatted string }{
				{`td."Outer Mixed"`, path.prefix + `"Outer Mixed"`}, {`td."a""b"`, path.prefix + `"a""b"`}, {"td.mood", path.prefix + "mood"}, {"td.pair", path.prefix + "pair"}, {"td.span", path.prefix + "span"}, {"td.spans", path.prefix + "spans"},
			} {
				typeDescribeValue(run, "pg_catalog.format_type('"+tc.name+"'::regtype::oid,NULL)", "TEXT", tc.formatted)
				typeDescribeValue(run, "format_type('"+tc.name+"[]'::regtype::oid,NULL)", "TEXT", tc.formatted+"[]")
			}
			want := path.prefix + "names"
			if path.set == "td_hidden,td,public" {
				want = "td.names"
			}
			typeDescribeValue(run, "format_type('td.names'::regtype::oid,NULL)", "TEXT", want)
			typeDescribeValue(run, "format_type('td.names'::regtype::oid,7)", "TEXT", want+"(7)")
			for _, tc := range []struct{ expression, want string }{{"format_type(23,NULL)", "integer"}, {"format_type(16,NULL)", "boolean"}, {"format_type(1114,NULL)", "timestamp without time zone"}, {"format_type(22,NULL)", "int2vector"}, {"format_type(30,NULL)", "oidvector"}, {"format_type(1006,NULL)", "int2vector[]"}, {"format_type(1013,NULL)", "oidvector[]"}} {
				typeDescribeValue(run, tc.expression, "TEXT", tc.want)
			}
		}
		run(fixture.Exec("SET search_path TO public", 0))
		run(fixture.Exec("CREATE FUNCTION public.format_type(oid,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'shadow'::text$$", 0))
		typeDescribeValue(run, "format_type(23::oid,NULL::integer)", "TEXT", "integer")
		run(fixture.Exec("SET search_path TO public,pg_catalog", 0))
		typeDescribeValue(run, "format_type(23::oid,NULL::integer)", "TEXT", "shadow")
		typeDescribeValue(run, "pg_catalog.format_type(23::oid,NULL::integer)", "TEXT", "integer")
	})
}
