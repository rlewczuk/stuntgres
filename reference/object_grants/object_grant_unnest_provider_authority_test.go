package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceObjectGrantArrayUnnestProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_array_unnest_provider(id INT,a INT[],m INT4MULTIRANGE)", 0))
		run(fixture.Exec("INSERT INTO og_array_unnest_provider VALUES(1,'{7,NULL,7}','{[1,3)}'),(2,'{}','{}'),(3,NULL,NULL)", 3))
		run(fixture.Exec("GRANT SELECT ON og_array_unnest_provider TO og_object_reader", 0))
		cases := []objectGrantProviderCase{
			{fixture.Select("SELECT unnest(ARRAY[7,NULL,7]) AS v ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}), true, true},
			{fixture.Select("SELECT v FROM pg_catalog.unnest(ARRAY[7,NULL,7]) AS u(v) ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}), true, true},
			{fixture.Select("SELECT unnest($1::INT[]) AS v ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}, "{7,NULL,7}"), true, true},
			{fixture.Select("SELECT v FROM unnest($1::INT[]) AS u(v) ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}, "{7,NULL,7}"), true, true},
			{fixture.Select("SELECT v,n FROM pg_catalog.unnest(ARRAY[9,NULL]) WITH ORDINALITY AS u(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}, {nil, int64(2)}}), true, true},
			{fixture.Select("SELECT unnest(a) AS v FROM og_array_unnest_provider WHERE id=1 ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}), true, true},
			{fixture.Select("SELECT u.v FROM og_array_unnest_provider s CROSS JOIN LATERAL unnest(s.a) AS u(v) WHERE s.id=1 ORDER BY u.v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}}), true, true},
		}
		for _, expr := range []string{"'{}'::INT[]", "NULL::INT[]"} {
			cases = append(cases,
				objectGrantProviderCase{fixture.Select("SELECT unnest("+expr+") AS v", []string{"v"}, []string{"INT4"}, nil), true, true},
				objectGrantProviderCase{fixture.Select("SELECT v FROM unnest("+expr+") AS u(v)", []string{"v"}, []string{"INT4"}, nil), true, true},
			)
		}
		for _, arg := range []any{"{}", nil} {
			cases = append(cases,
				objectGrantProviderCase{fixture.Select("SELECT unnest($1::INT[]) AS v", []string{"v"}, []string{"INT4"}, nil, arg), true, true},
				objectGrantProviderCase{fixture.Select("SELECT v FROM unnest($1::INT[]) AS u(v)", []string{"v"}, []string{"INT4"}, nil, arg), true, true},
			)
		}
		for _, q := range []string{
			"SELECT unnest(a) AS v FROM og_array_unnest_provider WHERE id=2",
			"SELECT unnest(a) AS v FROM og_array_unnest_provider WHERE id=3",
			"SELECT unnest(a) AS v FROM og_array_unnest_provider WHERE id>99",
			"SELECT u.v FROM og_array_unnest_provider s CROSS JOIN LATERAL unnest(s.a) AS u(v) WHERE s.id>99",
			"SELECT unnest(a) AS v FROM og_array_unnest_provider WHERE FALSE",
		} {
			cases = append(cases, objectGrantProviderCase{fixture.Select(q, []string{"v"}, []string{"INT4"}, nil), true, true})
		}
		for _, q := range []string{
			"SELECT v FROM unnest(ARRAY[7]) AS u(v) WHERE FALSE",
			"SELECT u.v FROM og_array_unnest_provider s CROSS JOIN LATERAL unnest(s.a) AS u(v) WHERE FALSE",
		} {
			cases = append(cases, objectGrantProviderCase{fixture.Select(q, []string{"v"}, []string{"INT4"}, nil), false, true})
		}
		// Same SQL name, distinct genuine formal identity. A single sibling is sufficient.
		control := fixture.Select("SELECT u.v FROM og_array_unnest_provider s CROSS JOIN LATERAL unnest(s.m) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}})
		control.RawTypes = []string{"RECORD"}
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.unnest(anyarray)", "function", "unnest", cases, []fixture.Step{control})
	})
}

func TestReferenceObjectGrantMultirangeUnnestProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_multirange_unnest_provider(id INT,m INT4MULTIRANGE)", 0))
		run(fixture.Exec("INSERT INTO og_multirange_unnest_provider VALUES(1,'{[1,3),[5,8)}'),(2,'{}'),(3,NULL)", 3))
		run(fixture.Exec("GRANT SELECT ON og_multirange_unnest_provider TO og_object_reader", 0))
		cases := []objectGrantProviderCase{
			{fixture.Select("SELECT unnest(CASE WHEN TRUE THEN '{[1,3),[5,8)}' ELSE m END) AS v FROM og_multirange_unnest_provider WHERE id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}), true, true},
			{fixture.Select("SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL pg_catalog.unnest(CASE WHEN TRUE THEN '{[1,3),[5,8)}' ELSE s.m END) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}), true, true},
			{fixture.Select("SELECT unnest(CASE WHEN TRUE THEN $1 ELSE m END) AS v FROM og_multirange_unnest_provider WHERE id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}, "{[1,3),[5,8)}"), true, true},
			{fixture.Select("SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(CASE WHEN TRUE THEN $1 ELSE s.m END) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}, "{[1,3),[5,8)}"), true, true},
			{fixture.Select("SELECT unnest(m) AS v FROM og_multirange_unnest_provider WHERE id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}), true, true},
			{fixture.Select("SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(s.m) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[5,8)"}}), true, true},
		}
		for _, expr := range []string{"'{}'", "NULL"} {
			cases = append(cases,
				objectGrantProviderCase{fixture.Select("SELECT unnest(CASE WHEN TRUE THEN "+expr+" ELSE m END) AS v FROM og_multirange_unnest_provider WHERE id=1", []string{"v"}, []string{"INT4RANGE"}, nil), true, true},
				objectGrantProviderCase{fixture.Select("SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(CASE WHEN TRUE THEN "+expr+" ELSE s.m END) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, nil), true, true},
			)
		}
		for _, arg := range []any{"{}", nil} {
			cases = append(cases,
				objectGrantProviderCase{fixture.Select("SELECT unnest(CASE WHEN TRUE THEN $1 ELSE m END) AS v FROM og_multirange_unnest_provider WHERE id=1", []string{"v"}, []string{"INT4RANGE"}, nil, arg), true, true},
				objectGrantProviderCase{fixture.Select("SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(CASE WHEN TRUE THEN $1 ELSE s.m END) AS u(v) WHERE s.id=1", []string{"v"}, []string{"INT4RANGE"}, nil, arg), true, true},
			)
		}
		for _, q := range []string{
			"SELECT unnest(m) AS v FROM og_multirange_unnest_provider WHERE id=2",
			"SELECT unnest(m) AS v FROM og_multirange_unnest_provider WHERE id=3",
			"SELECT unnest(m) AS v FROM og_multirange_unnest_provider WHERE id>99",
			"SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(s.m) AS u(v) WHERE s.id>99",
			"SELECT unnest(m) AS v FROM og_multirange_unnest_provider WHERE FALSE",
		} {
			cases = append(cases, objectGrantProviderCase{fixture.Select(q, []string{"v"}, []string{"INT4RANGE"}, nil), true, true})
		}
		for _, q := range []string{
			"SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(CASE WHEN TRUE THEN '{[1,3)}' ELSE s.m END) AS u(v) WHERE FALSE",
			"SELECT u.v FROM og_multirange_unnest_provider s CROSS JOIN LATERAL unnest(s.m) AS u(v) WHERE FALSE",
		} {
			cases = append(cases, objectGrantProviderCase{fixture.Select(q, []string{"v"}, []string{"INT4RANGE"}, nil), false, true})
		}
		// Preserve the real range transport contract without importing another package's helpers.
		for i := range cases {
			cases[i].step.RawTypes = []string{"RECORD"}
			cases[i].step.Bag = true
		}
		control := fixture.Select("SELECT v FROM unnest(ARRAY[7]) AS u(v)", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.unnest(anymultirange)", "function", "unnest", cases, []fixture.Step{control})
	})
}
