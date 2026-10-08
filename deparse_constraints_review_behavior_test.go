package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

var deparseReviewBehavior = []struct{ name, input, good, bad string }{
	{"false_endpoint", "FALSE BETWEEN (a<2) AND TRUE", "2", "1"},
	{"compared_between", "a BETWEEN 1 AND 3 = TRUE", "2", "4"},
	{"nonnull", "num_nonnulls(a,b,NULL)>0", "2", "NULL"},
	{"null_count", "num_nulls(a,b)=2", "NULL", "2"},
	{"round_cast", "round(a::numeric,1)>0", "2", "0"},
	{"bitwise", "(a & 3)=1", "1", "2"},
}

func TestDeparseConstraintsReviewNestedBehavior(t *testing.T) {
	for _, tc := range deparseReviewBehavior {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE original(a INT,b INT,CONSTRAINT c CHECK("+tc.input+"))")
			for i := 0; i < 2; i++ {
				var suffix string
				if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)).Scan(&suffix); err != nil {
					t.Fatal(err)
				}
				execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,CONSTRAINT c%d %s)", i, i, suffix))
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.good+",NULL)", 1)
					if tc.name != "null_count" && tc.name != "nonnull" {
						tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(NULL,NULL)", 1)
					}
					detail := tc.bad
					if detail == "NULL" {
						detail = "null"
					}
					checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.bad+",NULL)", checkViolation(name, constraint, detail+", null"))
					checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.good+",NULL),("+tc.bad+",NULL),("+tc.good+",NULL)", checkViolation(name, constraint, detail+", null"))
					checkExecError(t, db, prepared, "UPDATE "+name+" SET a="+tc.bad, checkViolation(name, constraint, detail+", null"))
				}
				count := int64(4)
				if tc.name == "null_count" || tc.name == "nonnull" {
					count = 2
				}
				wantTuples(t, db, "SELECT COUNT(*) FROM "+name, [][]any{{count}})
			}
		})
	}
}
