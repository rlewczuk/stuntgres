package stuntgres_test

import (
	"strings"
	"testing"

	"stuntgres"
)

func TestCountExpressionReviewRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "count-expression-review-regression")
	execSQL(t, db, "CREATE TABLE t(v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(NULL),(3)")
	execSQL(t, db, "CREATE TABLE empty(v INT)")

	// Independently specified from PostgreSQL REL_18_0 parse_func.c:
	// transformed children precede the 100-argument guard (134-146), signature
	// lookup (635-644), parameterless-name formatting (784-793), and aggregate
	// nesting analysis (parse_agg.c:675-698). The root's pinned PostgreSQL
	// 18.0 postgres_first direct/prepared checkpoint validated every record.
	// Full-record comparison also requires every unlisted field to be empty.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	args100 := strings.Repeat("1,", 99) + "1"
	args101 := strings.Repeat("1,", 100) + "1"
	for _, tc := range []struct {
		call string
		want stuntgres.Error
	}{
		{"pg_catalog.count()", stuntgres.Error{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"/*é*/ pg_catalog.count()", stuntgres.Error{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "14"}},
		{"count(count(*),missing)", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		{"count(count(*),1)", stuntgres.Error{Code: "42883", Message: "function count(bigint, integer) does not exist", Hint: hint, Position: "8"}},
		{"count(count(*))", stuntgres.Error{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"count(" + args100 + ")", stuntgres.Error{Code: "42883", Message: "function count(" + strings.Repeat("integer, ", 99) + "integer) does not exist", Hint: hint, Position: "8"}},
		{"count(" + args101 + ")", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"count(" + strings.Repeat("1,", 100) + "missing)", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "214"}},
		{"count(count(*)," + args100 + ")", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"count(count(*)," + strings.Repeat("1,", 99) + "missing)", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "221"}},
	} {
		for _, source := range []string{"t", "empty", "t WHERE FALSE", "empty WHERE FALSE"} {
			query := "SELECT " + tc.call + " FROM " + source
			t.Run(query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) {
					rows, err := db.Query(query)
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query exposed rows")
					}
					typeCoreDiagnostic(t, err, tc.want)
				})
				t.Run("prepared", func(t *testing.T) {
					stmt, err := db.Prepare(query)
					if err == nil {
						defer stmt.Close()
						rows, queryErr := stmt.Query()
						if rows != nil {
							rows.Close()
							t.Fatal("failed statement Query exposed rows")
						}
						err = queryErr
					} else if stmt != nil {
						stmt.Close()
						t.Fatal("failed Prepare exposed a statement")
					}
					typeCoreDiagnostic(t, err, tc.want)
				})
			})
		}
	}
}
