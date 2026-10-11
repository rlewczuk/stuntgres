package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Fixed full logical records, not expectations derived from native output or
// backend parity. PostgreSQL C implementation-source fields alone are excluded
// by the shared diagnostic fixture; every omitted logical field must be empty.
type queryAliasesDiagnosticCase struct {
	name, query string
	want        fixture.Diagnostic
}

func queryAliasesDiagnosticCases() []queryAliasesDiagnosticCase {
	return []queryAliasesDiagnosticCase{
		{"sibling_label", `SELECT id AS shown, shown+1 FROM t`, fixture.Diagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "21"}},
		{"where_label", `SELECT id AS shown FROM t WHERE shown=1`, fixture.Diagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "33"}},
		{"unicode_position", `SELECT /*é*/ id AS shown FROM t WHERE shown=1`, fixture.Diagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "39"}},
		{"compound_order_label", `SELECT id AS shown FROM t ORDER BY shown+0`, fixture.Diagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "36"}},
		{"renamed_unqualified", `SELECT id FROM t AS x(iid)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_qualified", `SELECT x.id FROM t AS x(iid)`, fixture.Diagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_unicode_position", `SELECT /*é*/ x.id FROM t AS x(iid)`, fixture.Diagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "14", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"two_candidates", `SELECT id FROM t AS x(iid,idx)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid" or the column "x.idx".`}},
		{"three_candidates_suppressed", `SELECT id FROM t AS x(iid,idx,idd)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8"}},
		{"distant_column_suppressed", `SELECT no_such_column FROM t AS x`, fixture.Diagnostic{Code: "42703", Message: `column "no_such_column" does not exist`, Position: "8"}},
		{"unicode_candidate", `SELECT x.id FROM t AS x("iíd")`, fixture.Diagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iíd".`}},
		{"hidden_original", `SELECT t.id FROM t AS x`, fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_schema_original", `SELECT public.t.id FROM public.t AS x`, fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_where_original", `SELECT x.id FROM t AS x WHERE t.id=1`, fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "31", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"duplicate_relation_alias", `SELECT * FROM t AS x CROSS JOIN t AS x`, fixture.Diagnostic{Code: "42712", Message: `table name "x" specified more than once`}},
		{"excess_column_aliases", `SELECT * FROM t AS x(i,j,k,z)`, fixture.Diagnostic{Code: "42P10", Message: `table "x" has 3 columns available but 4 columns specified`}},
		{"ambiguous_column_alias", `SELECT x.id FROM t AS x(id,id)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"ambiguous_output_order", `SELECT a AS x,b AS x FROM t ORDER BY x`, fixture.Diagnostic{Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38"}},
		{"malformed_column_alias", `SELECT x.id FROM t AS x(id,)`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "28"}},
		{"distant_qualifier_suppressed", `SELECT x.id FROM t x(renamed) CROSS JOIN u extraordinarilylongname`, fixture.Diagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8"}},
		// F1 complete records independently confirmed on pinned PostgreSQL18.0
		// before the exact-precedence production remedy.
		{"hidden_exact_single", `SELECT id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_self_join_alias", `SELECT id FROM (t AS l CROSS JOIN t AS r(other,c,d)) AS j(iid,a,b,other,c,d)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There is a column named "id" in table "l", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_unicode_position", `SELECT /*é*/ id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "14", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_multiple", `SELECT id FROM (t CROSS JOIN u) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		{"hidden_exact_three", `SELECT id FROM (t CROSS JOIN u AS v CROSS JOIN u AS w) AS j(iid,a,b,k,l)`, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		// F2 complete records independently confirmed on pinned PostgreSQL18.0
		// before the unconditional duplicate-scan production remedy.
		{"hidden_duplicate_exact", `SELECT id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_unicode_position", `SELECT /*é*/ id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "14"}},
		{"hidden_duplicate_after_plural", `SELECT id FROM (t CROSS JOIN u CROSS JOIN t AS x(id,id)) AS j(iid,a,b,k,l,c,d)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_qualifier_penalty", `SELECT j.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_distant_qualifier", `SELECT extraordinarilylongname.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS extraordinarilylongname(iid,a,b,k)`, fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
	}
}

func TestReferenceQueryAliasesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := queryAliasesDiagnosticCases()
	// Setup and assertions are inside each backend subtest, so /postgres has
	// no dependency on a native SQL assertion succeeding during the red phase.
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0),
				fixture.Exec("CREATE TABLE u(id INT)", 0),
			} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO u VALUES(1)", 1))
				}
				t.Run(population, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							step := fixture.ImplicitError(tc.query, tc.want)
							step.SelectRows, step.ErrorStage = true, "execute"
							t.Run("direct", func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
							})
							step.ErrorStage = "prepare"
							t.Run("prepare", func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
							})
						})
					}
				})
			}
		})
	}
}
