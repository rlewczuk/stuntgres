package stuntgres_test

import (
	"fmt"
	"strconv"
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
				var good any
				if tc.good != "NULL" {
					good, _ = strconv.ParseInt(tc.good, 10, 64)
				}
				retained := [][]any{}
				for _, prepared := range []bool{false, true} {
					tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.good+",NULL)", 1)
					retained = append(retained, []any{good, nil})
					if tc.name != "null_count" && tc.name != "nonnull" {
						tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(NULL,NULL)", 1)
						retained = append([][]any{{nil, nil}}, retained...)
					}
					detail := tc.bad
					if detail == "NULL" {
						detail = "null"
					}
					checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.bad+",NULL)", checkViolation(name, constraint, detail+", null"))
					wantTuples(t, db, "SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", retained)
					checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES("+tc.good+",NULL),("+tc.bad+",NULL),("+tc.good+",NULL)", checkViolation(name, constraint, detail+", null"))
					wantTuples(t, db, "SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", retained)
					checkExecError(t, db, prepared, "UPDATE "+name+" SET a="+tc.bad, checkViolation(name, constraint, detail+", null"))
					wantTuples(t, db, "SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", retained)
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

// Synthetic comparison annotations must not size the constant to n's typmod.
// The positive-scale goldens were independently observed on PostgreSQL 18.0;
// the negative-scale cases exercise the same base-type rule.
var deparseReviewNumericComparisons = []struct {
	name, declaration, input, compact, pretty, good, stored, bad, detail string
	overflow                                                             bool
}{
	{"less", "NUMERIC(4,2)", "n<1000", "(n < (1000)::numeric)", "n < 1000::numeric", "1", "1.00", "", "", false},
	{"reverse_less", "NUMERIC(4,2)", "1000>n", "((1000)::numeric > n)", "1000::numeric > n", "1", "1.00", "", "", false},
	{"distinct", "NUMERIC(4,2)", "n IS DISTINCT FROM 1000", "(n IS DISTINCT FROM (1000)::numeric)", "n IS DISTINCT FROM 1000::numeric", "1", "1.00", "", "", false},
	{"reverse_distinct", "NUMERIC(4,2)", "1000 IS DISTINCT FROM n", "((1000)::numeric IS DISTINCT FROM n)", "1000::numeric IS DISTINCT FROM n", "1", "1.00", "", "", false},
	{"false", "NUMERIC(4,2)", "n>1000", "(n > (1000)::numeric)", "n > 1000::numeric", "", "", "1", "1.00", false},
	{"reverse_false", "NUMERIC(4,2)", "1000<n", "((1000)::numeric < n)", "1000::numeric < n", "", "", "1", "1.00", false},
	{"explicit_overflow", "NUMERIC(4,2)", "n<CAST(1000 AS NUMERIC(4,2))", "(n < (1000)::numeric(4,2))", "n < 1000::numeric(4,2)", "", "", "1", "", true},
	{"explicit_success", "NUMERIC(4,2)", "n<CAST(10 AS NUMERIC(4,2))", "(n < (10)::numeric(4,2))", "n < 10::numeric(4,2)", "1", "1.00", "20", "20.00", false},
	{"negative_less", "NUMERIC(2,-1)", "n<10000", "(n < (10000)::numeric)", "n < 10000::numeric", "149", "150", "", "", false},
	{"negative_reverse_less", "NUMERIC(2,-1)", "10000>n", "((10000)::numeric > n)", "10000::numeric > n", "149", "150", "", "", false},
	{"negative_distinct", "NUMERIC(2,-1)", "n IS DISTINCT FROM 10000", "(n IS DISTINCT FROM (10000)::numeric)", "n IS DISTINCT FROM 10000::numeric", "149", "150", "", "", false},
	{"negative_reverse_distinct", "NUMERIC(2,-1)", "10000 IS DISTINCT FROM n", "((10000)::numeric IS DISTINCT FROM n)", "10000::numeric IS DISTINCT FROM n", "149", "150", "", "", false},
	{"negative_false", "NUMERIC(2,-1)", "n>10000", "(n > (10000)::numeric)", "n > 10000::numeric", "", "", "149", "150", false},
	{"negative_reverse_false", "NUMERIC(2,-1)", "10000<n", "((10000)::numeric < n)", "10000::numeric < n", "", "", "149", "150", false},
	{"negative_explicit_overflow", "NUMERIC(2,-1)", "n<CAST(10000 AS NUMERIC(2,-1))", "(n < (10000)::numeric(2,-1))", "n < 10000::numeric(2,-1)", "", "", "149", "", true},
	{"negative_explicit_success", "NUMERIC(2,-1)", "n<CAST(200 AS NUMERIC(2,-1))", "(n < (200)::numeric(2,-1))", "n < 200::numeric(2,-1)", "149", "150", "249", "250", false},
}

func TestDeparseConstraintsReviewNumericComparisonUsage(t *testing.T) {
	for _, tc := range deparseReviewNumericComparisons {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE original(id INT,n "+tc.declaration+",CONSTRAINT c CHECK("+tc.input+"))")
			for mode, expression := range []string{tc.compact, tc.pretty} {
				var actual string
				for _, inquiry := range []struct{ call, want string }{
					{fmt.Sprintf("pg_get_constraintdef(oid,%t)", mode == 1), "CHECK (" + expression + ")"},
					{fmt.Sprintf("pg_get_expr(conbin,conrelid,%t)", mode == 1), expression},
				} {
					deparseReviewTypedValue(t, db, "SELECT "+inquiry.call+" FROM pg_catalog.pg_constraint WHERE conname='c'", "TEXT", inquiry.want)
				}
				if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(id INT,n %s,CONSTRAINT c%d %s)", mode, tc.declaration, mode, actual))
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					execSQL(t, db, "DELETE FROM "+name)
					if tc.overflow {
						detail := "A field with precision 4, scale 2 must round to an absolute value less than 10^2."
						if tc.declaration == "NUMERIC(2,-1)" {
							detail = "A field with precision 2, scale -1 must round to an absolute value less than 10^3."
						}
						want := stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: detail}
						for _, values := range []string{"(1," + tc.bad + ")", "(1,NULL)", "(1,NULL),(2," + tc.bad + "),(3,NULL)"} {
							checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES"+values, want)
							wantTuples(t, db, "SELECT id,n FROM "+name+" ORDER BY id", [][]any{})
						}
						tableCoreExec(t, db, prepared, "UPDATE "+name+" SET n=1", 0)
						continue
					}
					tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(1,NULL)", 1)
					retained := [][]any{{int64(1), nil}}
					if tc.good != "" {
						tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(2,"+tc.good+")", 1)
						retained = append(retained, []any{int64(2), tc.stored})
						deparseReviewTypedValue(t, db, "SELECT n FROM "+name+" WHERE id=2", tc.declaration, tc.stored)
						if tc.declaration == "NUMERIC(4,2)" && tc.name != "explicit_success" {
							tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(6,99.99)", 1)
							retained = append(retained, []any{int64(6), "99.99"})
						}
					}
					wantTuples(t, db, "SELECT id,n FROM "+name+" ORDER BY id", retained)
					if tc.bad != "" {
						for _, query := range []string{
							"INSERT INTO " + name + " VALUES(3," + tc.bad + ")",
							"INSERT INTO " + name + " VALUES(4,NULL),(3," + tc.bad + "),(5,NULL)",
							"UPDATE " + name + " SET n=" + tc.bad + " WHERE id=1",
						} {
							id := "3"
							if query == "UPDATE "+name+" SET n="+tc.bad+" WHERE id=1" {
								id = "1"
							}
							checkExecError(t, db, prepared, query, checkViolation(name, constraint, id+", "+tc.detail))
							wantTuples(t, db, "SELECT id,n FROM "+name+" ORDER BY id", retained)
						}
					}
					rows, err := db.Query("SELECT n FROM " + name + " WHERE id=1")
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 {
						rows.Close()
						t.Fatal(types, err)
					}
					p, s, ok := types[0].DecimalSize()
					scale, precision := int64(2), int64(4)
					if tc.declaration == "NUMERIC(2,-1)" {
						scale, precision = -1, 2
					}
					if !ok || p != precision || s != scale || types[0].DatabaseTypeName() != "NUMERIC" {
						rows.Close()
						t.Fatal(types, p, s, ok)
					}
					rows.Close()
				}
			}
		})
	}
}
