package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// These independent candidate records are validated against pinned PostgreSQL
// before production implementation; they are not learned from native failures.
func TestReferenceDeleteUsingDiagnostics(t *testing.T) {
	cases := []struct{ name, query, code, message, token, detail, hint string }{
		{"duplicate", "DELETE FROM t USING t", "42712", `table name "t" specified more than once`, "", "", ""},
		{"alias_collision", "DELETE FROM t x USING s x", "42712", `table name "x" specified more than once`, "", "", ""},
		{"ambiguous_where", "DELETE FROM t USING s WHERE id=1", "42702", `column reference "id" is ambiguous`, "id=", "", ""},
		{"hidden_target", "DELETE FROM t x USING s WHERE t.id=s.id", "42P01", `invalid reference to FROM-clause entry for table "t"`, "t.id", "", `Perhaps you meant to reference the table alias "x".`},
		{"target_in_on", "DELETE FROM t x USING s JOIN r ON x.id=s.id", "42P01", `invalid reference to FROM-clause entry for table "x"`, "x.id", `There is an entry for table "x", but it cannot be referenced from this part of the query.`, ""},
		{"target_in_derived", "DELETE FROM t x USING (SELECT x.id) q", "42P01", `invalid reference to FROM-clause entry for table "x"`, "x.id", `There is an entry for table "x", but it cannot be referenced from this part of the query.`, ""},
		{"missing_source", "DELETE FROM t USING absent", "42P01", `relation "absent" does not exist`, "absent", "", ""},
		{"missing_qualifier", "DELETE FROM t USING s WHERE zz.id=1", "42P01", `missing FROM-clause entry for table "zz"`, "zz.id", "", ""},
		{"missing_column", "DELETE FROM t USING s WHERE s.absent=1", "42703", "column s.absent does not exist", "s.absent", "", ""},
		{"on_integer", "DELETE FROM t USING s JOIN r ON 1", "42804", "argument of JOIN/ON must be type boolean, not type integer", "1", "", ""},
		{"on_aggregate", "DELETE FROM t USING s JOIN r ON count(*)>0", "42803", "aggregate functions are not allowed in JOIN conditions", "count", "", ""},
		{"where_integer", "DELETE FROM t USING s WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "1", "", ""},
		{"using_duplicate", "DELETE FROM t USING s JOIN r USING(id,id)", "42701", `column name "id" appears more than once in USING clause`, "", "", ""},
		{"using_missing", "DELETE FROM t USING s JOIN r USING(absent)", "42703", `column "absent" specified in USING clause does not exist in left table`, "", "", ""},
		{"output_alias_collision", "DELETE FROM t USING s RETURNING WITH(OLD AS s) s.n", "42712", `table name "s" specified more than once`, "OLD", "", ""},
		{"output_only_old", "DELETE FROM t USING s WHERE old.id=1", "42P01", `missing FROM-clause entry for table "old"`, "old.id", "", ""},
		{"ambiguous_output", "DELETE FROM t USING s RETURNING id", "42702", `column reference "id" is ambiguous`, "id", "", ""},
		{"where_before_output", "DELETE FROM t USING s WHERE id=1 RETURNING absent", "42702", `column reference "id" is ambiguous`, "id=", "", ""},
		{"missing_source_syntax", "DELETE FROM t USING", "42601", "syntax error at end of input", "", "", ""},
		{"repeated_using", "DELETE FROM t USING s WHERE TRUE USING r", "42601", `syntax error at or near "USING"`, "USING r", "", ""},
		{"output_aggregate", "DELETE FROM t USING s RETURNING count(*)", "42803", "aggregate functions are not allowed in RETURNING", "count", "", ""},
	}
	for _, tc := range cases {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, populated := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					name := tc.name + prefix + map[bool]string{false: "/empty", true: "/populated"}[populated] + map[bool]string{false: "/execute", true: "/prepare"}[prepared]
					t.Run(name, func(t *testing.T) {
						deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := updateFromRun(t, p, b, c)
							run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
							run(fixture.Exec("CREATE TABLE s(id INT,n INT)", 0))
							run(fixture.Exec("CREATE TABLE r(id INT,z INT)", 0))
							var targets, sources, right [][]any
							if populated {
								run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
								run(fixture.Exec("INSERT INTO s VALUES(1,20)", 1))
								run(fixture.Exec("INSERT INTO r VALUES(1,30)", 1))
								targets = [][]any{{int64(1), int64(10)}}
								sources = [][]any{{int64(1), int64(20)}}
								right = [][]any{{int64(1), int64(30)}}
							}
							q := prefix + tc.query
							want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
							want.Detail, want.Hint = tc.detail, tc.hint
							if tc.token != "" {
								want.Position = updateFromPosition(q, tc.token)
							} else if tc.name == "missing_source_syntax" {
								want.Position = updateFromPosition(q+"!", "!")
							}
							step := fixture.ImplicitError(q, want)
							step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
							if prepared {
								step.ErrorStage = "prepare"
							}
							run(step)
							run(deleteUsingTargets(targets))
							run(fixture.Select("SELECT id,n FROM s", []string{"id", "n"}, []string{"INT4", "INT4"}, sources))
							run(fixture.Select("SELECT id,z FROM r", []string{"id", "z"}, []string{"INT4", "INT4"}, right))
						})
					})
				}
			}
		}
	}
}

func TestReferenceDeleteUsingPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"delete_using_denied"})
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(fixture.Exec("CREATE USER delete_using_denied", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO delete_using_denied", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_denied", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("DELETE FROM t d USING s WHERE d.id=s.k", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(deleteUsingTargets(deleteUsingOriginal()))
		deleteUsingSourcesUnchanged(run)
	})
}
