package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingQueryDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, q, code, message, token, detail, hint string }{
		{"hidden_source", "DELETE FROM t d USING s x WHERE s.k=d.id", "42P01", `invalid reference to FROM-clause entry for table "s"`, "s.k", "", `Perhaps you meant to reference the table alias "x".`},
		{"hidden_whole_join", "DELETE FROM t d USING (s JOIN r USING(k)) joined WHERE s.k=d.id", "42P01", `invalid reference to FROM-clause entry for table "s"`, "s.k", `There is an entry for table "s", but it cannot be referenced from this part of the query.`, ""},
		{"merged_alias_only", "DELETE FROM t d USING s JOIN r USING(k) AS merged WHERE merged.label='two'", "42703", "column merged.label does not exist", "merged.label", "", ""},
		{"column_alias_overflow", "DELETE FROM t USING s AS x(a,b,c)", "42P10", `table "x" has 2 columns available but 3 columns specified`, "", "", ""},
		{"missing_right_key", "DELETE FROM t USING s JOIN (SELECT 1 AS absent) r USING(k)", "42703", `column "k" specified in USING clause does not exist in right table`, "", "", ""},
		{"duplicate_exposed_key", "DELETE FROM t USING s AS x(k,k) JOIN r USING(k)", "42702", `common column name "k" appears more than once in left table`, "", "", ""},
		{"cross_on", "DELETE FROM t USING s CROSS JOIN r ON TRUE", "42601", `syntax error at or near "ON"`, "ON TRUE", "", ""},
		{"natural_on", "DELETE FROM t USING s NATURAL JOIN r ON TRUE", "42601", `syntax error at or near "ON"`, "ON TRUE", "", ""},
		{"empty_using_keys", "DELETE FROM t USING s JOIN r USING()", "42601", `syntax error at or near ")"`, ")", "", ""},
		{"quoted_hidden_target", `DELETE FROM t AS "Xé" USING s WHERE t.id=s.k`, "42P01", `invalid reference to FROM-clause entry for table "t"`, "t.id", "", `Perhaps you meant to reference the table alias "Xé".`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/execute", true: "/prepare"}[prepared], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					deleteUsingSeed(run)
					run(referenceExec("CREATE TABLE r(k INT)", 0))
					run(referenceExec("INSERT INTO r VALUES(2)", 1))
					q := "/*é*/ " + tc.q
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail, want.Hint = tc.detail, tc.hint
					if tc.token != "" {
						want.Position = updateFromPosition(q, tc.token)
					}
					updateFromDiagnostic(t, p.ctx, c, q, want, prepared)
					run(deleteUsingTargets(deleteUsingOriginal()))
					deleteUsingSourcesUnchanged(run)
					run(referenceSelect("SELECT k FROM r", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceDeleteUsingSourceTypes(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
		run(referenceExec("CREATE TABLE s(k INT)", 0))
		run(referenceExec("CREATE TABLE r(k BOOL)", 0))
		want := controlsReferenceDiagnostic("42883", "operator does not exist: integer = boolean")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		for _, prepared := range []bool{false, true} {
			updateFromDiagnostic(t, p.ctx, c, "DELETE FROM t USING s JOIN r USING(k)", want, prepared)
			run(deleteUsingTargets([][]any{{int64(1), int64(10)}}))
		}
	})
}

func TestReferenceDeleteUsingSchemaQualification(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(referenceExec("DELETE FROM public.t USING public.s WHERE public.t.id::bigint=public.s.k", 3))
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
	})
}
