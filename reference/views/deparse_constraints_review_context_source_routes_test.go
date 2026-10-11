package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewPrinterContextConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewContextCases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					db := b.DB
					db.SetMaxOpenConns(1)
					run := func(s fixture.Step) fixture.Observed {
						s.NonContext = true
						if s.SelectRows && s.Diagnostic == nil {
							previous := s.CheckMetadata
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
								fixture.DeparseReviewMetadata(t, b.PG, rows)
								deparseReviewNullableColumns(t, b.PG, rows, "a", "b", "c", "expected")
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE original(id INT,a INT,b INT,c INT,expected INT,CONSTRAINT ck CHECK("+tc.input+"))", 0))
					for mode, expr := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", mode == 1)
						step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
						var actual string
						if err := db.QueryRow(step.Query, step.Args...).Scan(&actual); err != nil || actual != step.Rows[0][0] {
							t.Fatal(actual, step.Rows, err)
						}
						run(step)
						run(fixture.Prepared(step))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,c INT,expected INT,CONSTRAINT copied%d %s)", mode, mode, actual), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "ck"
						if i > 0 {
							constraint = fmt.Sprintf("copied%d", i-1)
						}
						for _, prepared := range []bool{false, true} {
							nullValues := "NULL," + strings.Join(strings.Split(tc.good, ",")[1:], ",")
							step := fixture.Exec("INSERT INTO "+name+" VALUES(1,"+tc.good+"),(2,"+nullValues+")", 2)
							step.Prepared = prepared
							run(step)
							first := append([]any{int64(1)}, tc.goodRow...)
							second := append([]any{int64(2), nil}, tc.goodRow[1:]...)
							retained := [][]any{first, second}
							for _, op := range []struct{ query, id string }{
								{"INSERT INTO " + name + " VALUES(9," + tc.bad + ")", "9"},
								{"INSERT INTO " + name + " VALUES(8," + tc.good + "),(9," + tc.bad + "),(10," + tc.good + ")", "9"},
								{"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1", "1"},
							} {
								step := fixture.ImplicitError(op.query, fixture.CheckViolation(name, constraint, op.id+", "+tc.detail))
								step.Prepared = prepared
								run(step)
								run(fixture.Select("SELECT id,a,b,c,expected FROM "+name+" ORDER BY id", []string{"id", "a", "b", "c", "expected"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, retained))
							}
							if tc.division {
								query := "INSERT INTO " + name + " VALUES(9,12,0,0,1)"
								step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
								step.Prepared = prepared
								run(step)
								run(fixture.Select("SELECT id,a,b,c,expected FROM "+name+" ORDER BY id", []string{"id", "a", "b", "c", "expected"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, retained))
							}
							run(fixture.Exec("DELETE FROM "+name, 2))
						}
					}
				})
			}
		})
	}
}
