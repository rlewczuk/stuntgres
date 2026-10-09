package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var deparseReviewConditionalCases = []struct {
	name, input, compact, pretty, good, bad, assignment, detail string
	rows                                                        [][]any
	nullReject                                                  bool
}{
	{"searched_case", "CASE WHEN flag THEN a ELSE 2 END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,0,1,NULL),(4,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), int64(0), int64(1), nil}, {int64(4), nil, int64(1), true}}, false},
	{"simple_case", "CASE a WHEN 1 THEN true ELSE false END", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "(1,1,1,true),(2,1,2,false)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(1), int64(2), false}}, true},
	{"null_arm", "CASE WHEN flag THEN a END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,0,1,NULL),(4,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), int64(0), int64(1), nil}, {int64(4), nil, int64(1), true}}, false},
	{"not_distinct", "a IS NOT DISTINCT FROM b", "(NOT (a IS DISTINCT FROM b))", "NOT a IS DISTINCT FROM b", "(1,1,1,true),(2,NULL,NULL,false)", "1,2,true", "b=2", "1, 2, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), nil, nil, false}}, true},
	{"coalesce", "COALESCE(a,1)>0", "(COALESCE(a, 1) > 0)", "COALESCE(a, 1) > 0", "(1,1,1,true),(2,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), nil, int64(1), true}}, false},
	{"nullif", "NULLIF(a,1)>0", "(NULLIF(a, 1) > 0)", "NULLIF(a, 1) > 0", "(1,2,1,true),(2,1,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(2), int64(1), true}, {int64(2), int64(1), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"greatest", "GREATEST(a,-1)>0", "(GREATEST(a, '-1'::integer) > 0)", "GREATEST(a, '-1'::integer) > 0", "(1,1,1,true),(2,2,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(2), int64(1), true}}, true},
	{"least", "LEAST(a,3)>0", "(LEAST(a, 3) > 0)", "LEAST(a, 3) > 0", "(1,1,1,true),(2,4,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(4), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"arithmetic", "(a+1)*2>0", "(((a + 1) * 2) > 0)", "((a + 1) * 2) > 0", "(1,0,1,true),(2,1,1,true),(3,NULL,1,true)", "-1,1,true", "a=-1", "-1, 1, t", [][]any{{int64(1), int64(0), int64(1), true}, {int64(2), int64(1), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"divide_subtract", "(-a+(b-1)*2)/2>0", "((((- a) + ((b - 1) * 2)) / 2) > 0)", "(((- a) + (b - 1) * 2) / 2) > 0", "(1,-2,1,true),(2,-4,1,true),(3,NULL,1,true),(4,1,NULL,true)", "2,1,true", "a=2", "2, 1, t", [][]any{{int64(1), int64(-2), int64(1), true}, {int64(2), int64(-4), int64(1), true}, {int64(3), nil, int64(1), true}, {int64(4), int64(1), nil, true}}, false},
	{"remainder", "a%3=1", "((a % 3) = 1)", "(a % 3) = 1", "(1,1,1,true),(2,4,1,true),(3,NULL,1,true)", "2,1,true", "a=2", "2, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(4), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"bit_case", "CASE WHEN flag THEN (a&b) ELSE 1 END>0", "(\nCASE\n    WHEN flag THEN (a & b)\n    ELSE 1\nEND > 0)", "\nCASE\n    WHEN flag THEN a & b\n    ELSE 1\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), nil, int64(1), true}}, false},
	{"bit_func", "abs(a&b)>0", "(abs((a & b)) > 0)", "abs(a & b) > 0", "(1,1,1,true),(2,3,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(3), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
}

func TestReferenceDeparseConstraintsReviewConditionalConsumers(t *testing.T) {
	for _, tc := range deparseReviewConditionalCases {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, _ *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("CREATE TABLE original(id INT,a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
				for mode, expression := range []string{tc.compact, tc.pretty} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expression + ")"}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,flag BOOL,CONSTRAINT c%d %s)", mode, mode, actual), 0))
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "c"
					if i > 0 {
						constraint = fmt.Sprintf("c%d", i-1)
					}
					for _, prepared := range []bool{false, true} {
						step := referenceExec("INSERT INTO "+name+" VALUES"+tc.good, int64(len(tc.rows)))
						step.prepared = prepared
						run(step)
						snapshot := func() {
							run(referenceSelect("SELECT id,a,b,flag FROM "+name+" ORDER BY id", []string{"id", "a", "b", "flag"}, []string{"INT4", "INT4", "INT4", "BOOL"}, tc.rows))
						}
						goodFirst := strings.Replace(strings.Split(tc.good, ")")[0]+")", "(1,", "(8,", 1)
						for _, op := range []struct{ q, id string }{
							{"INSERT INTO " + name + " VALUES(9," + tc.bad + ")", "9"},
							{"INSERT INTO " + name + " VALUES" + goodFirst + ",(9," + tc.bad + ")," + strings.Replace(goodFirst, "(8,", "(10,", 1), "9"},
							{"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1", "1"},
						} {
							step := referenceImplicitError(op.q, referenceCheckViolation(name, constraint, op.id+", "+tc.detail))
							step.prepared = prepared
							run(step)
							snapshot()
						}
						extra := map[string]string{"simple_case": "2", "coalesce": "-1", "nullif": "-1", "greatest": "-2", "least": "-1", "arithmetic": "-2", "remainder": "-2"}[tc.name]
						if extra != "" {
							step := referenceImplicitError("INSERT INTO "+name+" VALUES(9,"+extra+",1,true)", referenceCheckViolation(name, constraint, "9, "+extra+", 1, t"))
							step.prepared = prepared
							run(step)
							snapshot()
						}
						if tc.nullReject {
							step := referenceImplicitError("INSERT INTO "+name+" VALUES(9,NULL,1,true)", referenceCheckViolation(name, constraint, "9, null, 1, t"))
							step.prepared = prepared
							run(step)
							snapshot()
						}
						run(referenceExec("DELETE FROM "+name, int64(len(tc.rows))))
					}
				}
			})
		})
	}
}

func TestReferenceDeparseConstraintsReviewGreatestFloorConsumers(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, _ *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE TABLE original(id INT,a INT,CONSTRAINT c CHECK(GREATEST(a,1)>0))", 0))
		for mode, expr := range []string{"(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"} {
			q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
			step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
			actual := run(step).rows[0][0].(string)
			run(referencePrepared(step))
			run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,CONSTRAINT copied %s)", mode, actual), 0))
		}
		for _, name := range []string{"original", "copy0", "copy1"} {
			for _, prepared := range []bool{false, true} {
				step := referenceExec("INSERT INTO "+name+" VALUES(1,-100),(2,0),(3,NULL)", 3)
				step.prepared = prepared
				run(step)
				run(referenceSelect("SELECT id,a FROM "+name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-100)}, {int64(2), int64(0)}, {int64(3), nil}}))
				run(referenceExec("DELETE FROM "+name, 3))
			}
		}
	})
}
