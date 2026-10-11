package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateFromDiagnostics(t *testing.T) {
	cases := []struct{ name, q, code, message, token, detail, hint string }{
		{"duplicate", "UPDATE t SET v=1 FROM t", "42712", "table name \"t\" specified more than once", "", "", ""},
		{"alias_collision", "UPDATE t x SET v=1 FROM s x", "42712", "table name \"x\" specified more than once", "", "", ""},
		{"ambiguous_rhs", "UPDATE t SET v=id FROM s", "42702", "column reference \"id\" is ambiguous", "id", "", ""},
		{"ambiguous_where", "UPDATE t SET v=1 FROM s WHERE id=1", "42702", "column reference \"id\" is ambiguous", "id", "", ""},
		{"hidden_target", "UPDATE t x SET v=t.v FROM s", "42P01", "invalid reference to FROM-clause entry for table \"t\"", "t.v", "", "Perhaps you meant to reference the table alias \"x\"."},
		{"target_in_on", "UPDATE t x SET v=s.n FROM s JOIN r ON x.id=s.id", "42P01", "invalid reference to FROM-clause entry for table \"x\"", "x.id", "There is an entry for table \"x\", but it cannot be referenced from this part of the query.", ""},
		{"target_in_parenthesized_on", "UPDATE t x SET v=s.n FROM (s JOIN r ON x.id=s.id)", "42P01", "invalid reference to FROM-clause entry for table \"x\"", "x.id", "There is an entry for table \"x\", but it cannot be referenced from this part of the query.", ""},
		{"missing_qualifier", "UPDATE t SET v=z.n FROM s", "42P01", "missing FROM-clause entry for table \"z\"", "z.n", "", ""},
		{"missing_column", "UPDATE t SET v=s.absent FROM s", "42703", "column s.absent does not exist", "s.absent", "", ""},
		{"qualified_set", "UPDATE t SET t.v=1 FROM s", "42703", "column \"t\" of relation \"t\" does not exist", "t.v", "", "SET target columns cannot be qualified with the relation name."},
		{"qualified_alias_set", "UPDATE t x SET x.v=1 FROM s", "42703", "column \"x\" of relation \"t\" does not exist", "x.v", "", "SET target columns cannot be qualified with the relation name."},
		{"missing_source", "UPDATE t SET v=1 FROM absent", "42P01", "relation \"absent\" does not exist", "absent", "", ""},
		{"on_boolean", "UPDATE t SET v=0 FROM s JOIN r ON 1", "42804", "argument of JOIN/ON must be type boolean, not type integer", "1", "", ""},
		{"on_aggregate", "UPDATE t SET v=1 FROM s JOIN r ON count(*)>0", "42803", "aggregate functions are not allowed in JOIN conditions", "count", "", ""},
		{"using_missing", "UPDATE t SET v=1 FROM s JOIN r USING(absent)", "42703", "column \"absent\" specified in USING clause does not exist in left table", "", "", ""},
		{"using_duplicate", "UPDATE t SET v=1 FROM s JOIN r USING(id,id)", "42701", "column name \"id\" appears more than once in USING clause", "", "", ""},
		{"on_before_set", "UPDATE t SET absent=1 FROM s JOIN r ON FALSE::int", "42804", "argument of JOIN/ON must be type boolean, not type integer", "FALSE", "", ""},
		{"where_before_output", "UPDATE t SET v=1 FROM s WHERE id=1 RETURNING absent", "42702", "column reference \"id\" is ambiguous", "id", "", ""},
		{"output_alias_collision", "UPDATE t SET v=s.n FROM s RETURNING WITH (OLD AS s) s.n", "42712", "table name \"s\" specified more than once", "OLD", "", ""},
		{"quoted_alias", "UPDATE t AS \"Xé\" SET v=t.v FROM s", "42P01", "invalid reference to FROM-clause entry for table \"t\"", "t.v", "", "Perhaps you meant to reference the table alias \"Xé\"."},
	}
	for _, tc := range cases {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, populated := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					name := tc.name + prefix + map[bool]string{false: "/empty", true: "/populated"}[populated] + map[bool]string{false: "/exec", true: "/prepare"}[prepared]
					t.Run(name, func(t *testing.T) {
						updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := updateFromRun(t, p, b, c)
							run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
							run(fixture.Exec("CREATE TABLE s(id INT,n INT)", 0))
							run(fixture.Exec("CREATE TABLE r(id INT,z INT)", 0))
							var before [][]any
							if populated {
								run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
								run(fixture.Exec("INSERT INTO s VALUES(1,101)", 1))
								run(fixture.Exec("INSERT INTO r VALUES(1,9)", 1))
								before = [][]any{{int64(1), int64(10)}}
							}
							q := prefix + tc.q
							want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
							want.Detail = tc.detail
							want.Hint = tc.hint
							if tc.token != "" {
								want.Position = updateFromPosition(q, tc.token)
							}
							updateFromDiagnostic(t, p.Ctx, c, q, want, prepared)
							run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, before))
						})
					})
				}
			}
		}
	}
}
