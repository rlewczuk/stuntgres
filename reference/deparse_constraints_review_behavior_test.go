package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
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

func TestReferenceDeparseConstraintsReviewNestedBehavior(t *testing.T) {
	for _, tc := range deparseReviewBehavior {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, _ *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("CREATE TABLE original(a INT,b INT,CONSTRAINT c CHECK("+tc.input+"))", 0))
				// No source expression substitution: collect actual suffixes directly through
				// the public driver; the exact renderer matrix independently checks goldens.
				for i := 0; i < 2; i++ {
					query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)
					// This scenario uses independently derived goldens for the extra predicates.
					compact, pretty := deparseReviewBehaviorGolden(tc.name)
					want := compact
					if i == 1 {
						want = pretty
					}
					observed := run(referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}}))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,CONSTRAINT c%d %s)", i, i, observed.rows[0][0].(string)), 0))
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
						step := referenceExec("INSERT INTO "+name+" VALUES("+tc.good+",NULL)", 1)
						step.prepared = prepared
						run(step)
						retained = append(retained, []any{good, nil})
						if tc.name != "null_count" && tc.name != "nonnull" {
							step = referenceExec("INSERT INTO "+name+" VALUES(NULL,NULL)", 1)
							step.prepared = prepared
							run(step)
							retained = append([][]any{{nil, nil}}, retained...)
						}
						detail := tc.bad
						if detail == "NULL" {
							detail = "null"
						}
						for _, query := range []string{"INSERT INTO " + name + " VALUES(" + tc.bad + ",NULL)", "INSERT INTO " + name + " VALUES(" + tc.good + ",NULL),(" + tc.bad + ",NULL),(" + tc.good + ",NULL)", "UPDATE " + name + " SET a=" + tc.bad} {
							step = referenceImplicitError(query, referenceCheckViolation(name, constraint, detail+", null"))
							step.prepared = prepared
							run(step)
							run(referenceSelect("SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, retained))
						}
					}
					count := int64(4)
					if tc.name == "null_count" || tc.name == "nonnull" {
						count = 2
					}
					run(referenceSelect("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				}
			})
		})
	}
}
func deparseReviewBehaviorGolden(name string) (string, string) {
	switch name {
	case "false_endpoint":
		return "CHECK (((false >= (a < 2)) AND (false <= true)))", "CHECK (false >= (a < 2) AND false <= true)"
	case "compared_between":
		return "CHECK ((((a >= 1) AND (a <= 3)) = true))", "CHECK ((a >= 1 AND a <= 3) = true)"
	case "nonnull":
		return "CHECK ((num_nonnulls(a, b, NULL::unknown) > 0))", "CHECK (num_nonnulls(a, b, NULL::unknown) > 0)"
	case "null_count":
		return "CHECK ((num_nulls(a, b) = 2))", "CHECK (num_nulls(a, b) = 2)"
	case "round_cast":
		return "CHECK ((round((a)::numeric, 1) > (0)::numeric))", "CHECK (round(a::numeric, 1) > 0::numeric)"
	case "bitwise":
		return "CHECK (((a & 3) = 1))", "CHECK ((a & 3) = 1)"
	}
	panic("unrecognized independently specified case")
}
