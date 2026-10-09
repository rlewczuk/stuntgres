package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewPrinterContextConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewContextCases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					db := b.db
					db.SetMaxOpenConns(1)
					run := func(s referenceStep) referenceObserved {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							previous := s.checkMetadata
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
								deparseReviewMetadata(t, b.pg, rows)
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return referenceRunStep(t, p.ctx, db, b.recorder, s)
					}
					run(referenceExec("CREATE TABLE original(id INT,a INT,b INT,c INT,expected INT,CONSTRAINT ck CHECK("+tc.input+"))", 0))
					for mode, expr := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", mode == 1)
						step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
						var actual string
						if err := db.QueryRow(step.query, step.args...).Scan(&actual); err != nil || actual != step.rows[0][0] {
							t.Fatal(actual, step.rows, err)
						}
						run(step)
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
			}
		})
	}
}
