package stuntgres_test

import (
	"errors"
	"testing"

	"stuntgres"
)

// Expectations are specified independently of either backend. The reference
// regression checks these records against pinned PostgreSQL 18.0 before remedy.
func TestQueryAliasesDiagnostics(t *testing.T) {
	cases := []struct {
		name, query string
		want        stuntgres.Error
	}{
		{"sibling_label", `SELECT id AS shown, shown+1 FROM t`, stuntgres.Error{Code: "42703", Message: `column "shown" does not exist`, Position: "21"}},
		{"where_label", `SELECT id AS shown FROM t WHERE shown=1`, stuntgres.Error{Code: "42703", Message: `column "shown" does not exist`, Position: "33"}},
		{"unicode_position", `SELECT /*é*/ id AS shown FROM t WHERE shown=1`, stuntgres.Error{Code: "42703", Message: `column "shown" does not exist`, Position: "39"}},
		{"compound_order_label", `SELECT id AS shown FROM t ORDER BY shown+0`, stuntgres.Error{Code: "42703", Message: `column "shown" does not exist`, Position: "36"}},
		{"renamed_unqualified", `SELECT id FROM t AS x(iid)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_qualified", `SELECT x.id FROM t AS x(iid)`, stuntgres.Error{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"renamed_unicode_position", `SELECT /*é*/ x.id FROM t AS x(iid)`, stuntgres.Error{Code: "42703", Message: `column x.id does not exist`, Position: "14", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"two_candidates", `SELECT id FROM t AS x(iid,idx)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid" or the column "x.idx".`}},
		{"three_candidates_suppressed", `SELECT id FROM t AS x(iid,idx,idd)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8"}},
		{"distant_column_suppressed", `SELECT no_such_column FROM t AS x`, stuntgres.Error{Code: "42703", Message: `column "no_such_column" does not exist`, Position: "8"}},
		{"unicode_candidate", `SELECT x.id FROM t AS x("iíd")`, stuntgres.Error{Code: "42703", Message: `column x.id does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iíd".`}},
		{"hidden_original", `SELECT t.id FROM t AS x`, stuntgres.Error{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_schema_original", `SELECT public.t.id FROM public.t AS x`, stuntgres.Error{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"hidden_where_original", `SELECT x.id FROM t AS x WHERE t.id=1`, stuntgres.Error{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "31", Hint: `Perhaps you meant to reference the table alias "x".`}},
		{"duplicate_relation_alias", `SELECT * FROM t AS x CROSS JOIN t AS x`, stuntgres.Error{Code: "42712", Message: `table name "x" specified more than once`}},
		{"excess_column_aliases", `SELECT * FROM t AS x(i,j,k,z)`, stuntgres.Error{Code: "42P10", Message: `table "x" has 3 columns available but 4 columns specified`}},
		{"ambiguous_column_alias", `SELECT x.id FROM t AS x(id,id)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"ambiguous_output_order", `SELECT a AS x,b AS x FROM t ORDER BY x`, stuntgres.Error{Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38"}},
		{"malformed_column_alias", `SELECT x.id FROM t AS x(id,)`, stuntgres.Error{Code: "42601", Message: `syntax error at or near ")"`, Position: "28"}},
		{"distant_qualifier_suppressed", `SELECT x.id FROM t x(renamed) CROSS JOIN u extraordinarilylongname`, stuntgres.Error{Code: "42703", Message: `column x.id does not exist`, Position: "8"}},
		// F1 complete records independently confirmed on pinned PostgreSQL18.0
		// before the exact-precedence production remedy.
		{"hidden_exact_single", `SELECT id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_unicode_position", `SELECT /*é*/ id FROM (t CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "14", Detail: `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`}},
		{"hidden_exact_multiple", `SELECT id FROM (t CROSS JOIN u) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		{"hidden_exact_three", `SELECT id FROM (t CROSS JOIN u AS v CROSS JOIN u AS w) AS j(iid,a,b,k,l)`, stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		// F2 complete records independently confirmed on pinned PostgreSQL18.0
		// before the unconditional duplicate-scan production remedy.
		{"hidden_duplicate_exact", `SELECT id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_unicode_position", `SELECT /*é*/ id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "14"}},
		{"hidden_duplicate_after_plural", `SELECT id FROM (t CROSS JOIN u CROSS JOIN t AS x(id,id)) AS j(iid,a,b,k,l,c,d)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_qualifier_penalty", `SELECT j.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS j(iid,a,b,k)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"hidden_duplicate_distant_qualifier", `SELECT extraordinarilylongname.id FROM (t AS x(id,id) CROSS JOIN u AS v(k)) AS extraordinarilylongname(iid,a,b,k)`, stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
	}
	for _, population := range []string{"empty", "populated"} {
		t.Run(population, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			execSQL(t, db, "CREATE TABLE u(id INT)")
			if population == "populated" {
				execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
				execSQL(t, db, "INSERT INTO u VALUES(1)")
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := tc.want
					want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
					for _, mode := range []string{"direct", "prepare"} {
						t.Run(mode, func(t *testing.T) {
							var err error
							if mode == "direct" {
								rows, queryErr := db.Query(tc.query)
								err = queryErr
								if rows != nil {
									rows.Close()
									t.Fatal("failed Query exposed rows")
								}
							} else {
								stmt, prepareErr := db.Prepare(tc.query)
								err = prepareErr
								if stmt != nil {
									stmt.Close()
									t.Fatal("failed Prepare exposed a statement")
								}
							}
							var got *stuntgres.Error
							if !errors.As(err, &got) || *got != want {
								t.Fatalf("%s: complete diagnostic got %#v (%v), want %#v", tc.query, got, err, want)
							}
						})
					}
				})
			}
		})
	}
}
