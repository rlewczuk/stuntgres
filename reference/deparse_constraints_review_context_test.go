package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var deparseReviewContextCases = []struct {
	name, input, compact, pretty, good, bad, assignment, detail string
	goodRow                                                     []any
	division                                                    bool
}{
	{"left_sub", "(a-b)-c=expected", "(((a - b) - c) = expected)", "(a - b - c) = expected", "10,6,2,2", "10,6,2,6", "expected=6", "10, 6, 2, 6", []any{int64(10), int64(6), int64(2), int64(2)}, false},
	{"right_sub", "a-(b-c)=expected", "((a - (b - c)) = expected)", "(a - (b - c)) = expected", "10,6,2,6", "10,6,2,2", "expected=2", "10, 6, 2, 2", []any{int64(10), int64(6), int64(2), int64(6)}, false},
	{"left_div", "(a/b)/c=expected", "(((a / b) / c) = expected)", "(a / b / c) = expected", "12,6,2,1", "12,6,2,4", "expected=4", "12, 6, 2, 4", []any{int64(12), int64(6), int64(2), int64(1)}, true},
	{"right_div", "a/(b/c)=expected", "((a / (b / c)) = expected)", "(a / (b / c)) = expected", "12,6,2,4", "12,6,2,1", "expected=1", "12, 6, 2, 1", []any{int64(12), int64(6), int64(2), int64(4)}, true},
	{"abs_arg", "abs(a+b)>0", "(abs((a + b)) > 0)", "abs(a + b) > 0", "1,1,1,0", "1,-1,1,0", "b=-1", "1, -1, 1, 0", []any{int64(1), int64(1), int64(1), int64(0)}, false},
	{"explicit_cast", "CAST(a+b AS bigint)>0", "(((a + b))::bigint > 0)", "(a + b)::bigint > 0", "1,1,1,0", "1,-1,1,0", "b=-1", "1, -1, 1, 0", []any{int64(1), int64(1), int64(1), int64(0)}, false},
	{"bit_in_math", "a+(b&c)>0", "((a + (b & c)) > 0)", "(a + (b & c)) > 0", "0,1,1,0", "-1,1,1,0", "a=-1", "-1, 1, 1, 0", []any{int64(0), int64(1), int64(1), int64(0)}, false},
	{"math_in_bit", "((a+b)&c)>0", "(((a + b) & c) > 0)", "((a + b) & c) > 0", "0,1,1,0", "-1,1,1,0", "a=-1", "-1, 1, 1, 0", []any{int64(0), int64(1), int64(1), int64(0)}, false},
	{"unary_in_math", "-a+b>0", "(((- a) + b) > 0)", "((- a) + b) > 0", "0,1,1,0", "1,1,1,0", "a=1", "1, 1, 1, 0", []any{int64(0), int64(1), int64(1), int64(0)}, false},
	{"double_unary", "-(-a)>0", "((- (- a)) > 0)", "(- (- a)) > 0", "1,1,1,0", "0,1,1,0", "a=0", "0, 1, 1, 0", []any{int64(1), int64(1), int64(1), int64(0)}, false},
}

func TestReferenceDeparseConstraintsReviewPrinterContextConsumers(t *testing.T) {
	for _, tc := range deparseReviewContextCases {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, _ *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("CREATE TABLE original(id INT,a INT,b INT,c INT,expected INT,CONSTRAINT ck CHECK("+tc.input+"))", 0))
				for mode, expr := range []string{tc.compact, tc.pretty} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", mode == 1)
					step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,c INT,expected INT,CONSTRAINT copied%d %s)", mode, mode, actual), 0))
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "ck"
					if i > 0 {
						constraint = fmt.Sprintf("copied%d", i-1)
					}
					for _, prepared := range []bool{false, true} {
						nullValues := "NULL," + strings.Join(strings.Split(tc.good, ",")[1:], ",")
						step := referenceExec("INSERT INTO "+name+" VALUES(1,"+tc.good+"),(2,"+nullValues+")", 2)
						step.prepared = prepared
						run(step)
						first := append([]any{int64(1)}, tc.goodRow...)
						second := append([]any{int64(2), nil}, tc.goodRow[1:]...)
						retained := [][]any{first, second}
						for _, op := range []struct{ query, id string }{
							{"INSERT INTO " + name + " VALUES(9," + tc.bad + ")", "9"},
							{"INSERT INTO " + name + " VALUES(8," + tc.good + "),(9," + tc.bad + "),(10," + tc.good + ")", "9"},
							{"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1", "1"},
						} {
							step := referenceImplicitError(op.query, referenceCheckViolation(name, constraint, op.id+", "+tc.detail))
							step.prepared = prepared
							run(step)
							run(referenceSelect("SELECT id,a,b,c,expected FROM "+name+" ORDER BY id", []string{"id", "a", "b", "c", "expected"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, retained))
						}
						if tc.division {
							query := "INSERT INTO " + name + " VALUES(9,12,0,0,1)"
							step := referenceImplicitError(query, referenceDiagnostic{Code: "22012", Message: "division by zero"})
							step.prepared = prepared
							run(step)
							run(referenceSelect("SELECT id,a,b,c,expected FROM "+name+" ORDER BY id", []string{"id", "a", "b", "c", "expected"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, retained))
						}
						run(referenceExec("DELETE FROM "+name, 2))
					}
				}
			})
		})
	}
}
