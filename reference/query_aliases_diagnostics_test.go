//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

// Fixed full logical records, not expectations derived from native output or
// backend parity. PostgreSQL C implementation-source fields alone are excluded
// by the shared diagnostic fixture; every omitted logical field must be empty.
func TestReferenceQueryAliasesDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	cases := []struct {
		name, query string
		want        referenceDiagnostic
	}{
		{"sibling_label", `SELECT id AS shown, shown+1 FROM t`, referenceDiagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "21"}},
		{"where_label", `SELECT id AS shown FROM t WHERE shown=1`, referenceDiagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "33"}},
		{"unicode_position", `SELECT /*é*/ id AS shown FROM t WHERE shown=1`, referenceDiagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "39"}},
		{"compound_order_label", `SELECT id AS shown FROM t ORDER BY shown+0`, referenceDiagnostic{Code: "42703", Message: `column "shown" does not exist`, Position: "36"}},
		{"renamed_unqualified", `SELECT id FROM t AS x(iid)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_qualified", `SELECT x.id FROM t AS x(iid)`, referenceDiagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_unicode_position", `SELECT /*é*/ x.id FROM t AS x(iid)`, referenceDiagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "14", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"two_candidates", `SELECT id FROM t AS x(iid,idx)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid" or the column "x.idx".`}},
		{"three_candidates_suppressed", `SELECT id FROM t AS x(iid,idx,idd)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8"}},
		{"distant_column_suppressed", `SELECT no_such_column FROM t AS x`, referenceDiagnostic{Code: "42703", Message: `column "no_such_column" does not exist`, Position: "8"}},
		{"unicode_candidate", `SELECT x.id FROM t AS x("iíd")`, referenceDiagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iíd".`}},
		{"hidden_original", `SELECT t.id FROM t AS x`, referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_schema_original", `SELECT public.t.id FROM public.t AS x`, referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_where_original", `SELECT x.id FROM t AS x WHERE t.id=1`, referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "31", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"duplicate_relation_alias", `SELECT * FROM t AS x CROSS JOIN t AS x`, referenceDiagnostic{Code: "42712", Message: `table name "x" specified more than once`}},
		{"excess_column_aliases", `SELECT * FROM t AS x(i,j,k,z)`, referenceDiagnostic{Code: "42P10", Message: `table "x" has 3 columns available but 4 columns specified`}},
		{"ambiguous_column_alias", `SELECT x.id FROM t AS x(id,id)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"ambiguous_output_order", `SELECT a AS x,b AS x FROM t ORDER BY x`, referenceDiagnostic{Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38"}},
		{"malformed_column_alias", `SELECT x.id FROM t AS x(id,)`, referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "28"}},
		{"distant_qualifier_suppressed", `SELECT x.id FROM t x(renamed) CROSS JOIN u extraordinarilylongname`, referenceDiagnostic{Code: "42703", Message: `column x.id does not exist`, Position: "8"}},
		// F1 complete records independently confirmed on pinned PostgreSQL18.0
		// before the exact-precedence production remedy.
		{"hidden_exact_single", `SELECT id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_unicode_position", `SELECT /*é*/ id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "14", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_multiple", `SELECT id FROM (t CROSS JOIN u) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		{"hidden_exact_three", `SELECT id FROM (t CROSS JOIN u AS v CROSS JOIN u AS w) AS j(iid,a,b,k,l)`, referenceDiagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		// F2 complete records independently confirmed on pinned PostgreSQL18.0
		// before the unconditional duplicate-scan production remedy.
		{"hidden_duplicate_exact", `SELECT id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_unicode_position", `SELECT /*é*/ id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "14"}},
		{"hidden_duplicate_after_plural", `SELECT id FROM (t CROSS JOIN u CROSS JOIN t AS x(id,id)) AS j(iid,a,b,k,l,c,d)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_qualifier_penalty", `SELECT j.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_distant_qualifier", `SELECT extraordinarilylongname.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS extraordinarilylongname(iid,a,b,k)`, referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
	}
	// Setup and assertions are inside each backend subtest, so /postgres has
	// no dependency on a native SQL assertion succeeding during the red phase.
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0),
				referenceExec("CREATE TABLE u(id INT)", 0),
			} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO u VALUES(1)", 1))
				}
				t.Run(population, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							step := referenceImplicitError(tc.query, tc.want)
							step.selectRows, step.errorStage = true, "execute"
							t.Run("direct", func(t *testing.T) {
								referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
							})
							step.errorStage = "prepare"
							t.Run("prepare", func(t *testing.T) {
								referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
							})
						})
					}
				})
			}
		})
	}
}
