package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewConditionalConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewConditionalCases {
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
								deparseReviewNullableColumns(t, b.PG, rows, "a", "b", "flag")
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE original(id INT,a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
					for mode, expression := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
						step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expression + ")"}})
						var actual string
						if err := db.QueryRow(step.Query, step.Args...).Scan(&actual); err != nil || actual != step.Rows[0][0] {
							t.Fatal(actual, step.Rows, err)
						}
						run(step)
						run(fixture.Prepared(step))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,flag BOOL,CONSTRAINT c%d %s)", mode, mode, actual), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "c"
						if i > 0 {
							constraint = fmt.Sprintf("c%d", i-1)
						}
						for _, prepared := range []bool{false, true} {
							step := fixture.Exec("INSERT INTO "+name+" VALUES"+tc.good, int64(len(tc.rows)))
							step.Prepared = prepared
							run(step)
							snapshot := func() {
								run(fixture.Select("SELECT id,a,b,flag FROM "+name+" ORDER BY id", []string{"id", "a", "b", "flag"}, []string{"INT4", "INT4", "INT4", "BOOL"}, tc.rows))
							}
							goodFirst := strings.Replace(strings.Split(tc.good, ")")[0]+")", "(1,", "(8,", 1)
							for _, op := range []struct{ q, id string }{
								{"INSERT INTO " + name + " VALUES(9," + tc.bad + ")", "9"},
								{"INSERT INTO " + name + " VALUES" + goodFirst + ",(9," + tc.bad + ")," + strings.Replace(goodFirst, "(8,", "(10,", 1), "9"},
								{"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1", "1"},
							} {
								step := fixture.ImplicitError(op.q, fixture.CheckViolation(name, constraint, op.id+", "+tc.detail))
								step.Prepared = prepared
								run(step)
								snapshot()
							}
							extra := map[string]string{"simple_case": "2", "coalesce": "-1", "nullif": "-1", "greatest": "-2", "least": "-1", "arithmetic": "-2", "remainder": "-2"}[tc.name]
							if extra != "" {
								step := fixture.ImplicitError("INSERT INTO "+name+" VALUES(9,"+extra+",1,true)", fixture.CheckViolation(name, constraint, "9, "+extra+", 1, t"))
								step.Prepared = prepared
								run(step)
								snapshot()
							}
							if tc.nullReject {
								step := fixture.ImplicitError("INSERT INTO "+name+" VALUES(9,NULL,1,true)", fixture.CheckViolation(name, constraint, "9, null, 1, t"))
								step.Prepared = prepared
								run(step)
								snapshot()
							}
							run(fixture.Exec("DELETE FROM "+name, int64(len(tc.rows))))
						}
					}
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsReviewGreatestFloorConsumersSourceConsumerRoutes(t *testing.T) {
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
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE original(id INT,a INT,CONSTRAINT c CHECK(GREATEST(a,1)>0))", 0))
			for mode, expr := range []string{"(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"} {
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
				var actual string
				if err := db.QueryRow(step.Query, step.Args...).Scan(&actual); err != nil || actual != step.Rows[0][0] {
					t.Fatal(actual, step.Rows, err)
				}
				run(step)
				run(fixture.Prepared(step))
				run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,CONSTRAINT copied %s)", mode, actual), 0))
			}
			for _, name := range []string{"original", "copy0", "copy1"} {
				for _, prepared := range []bool{false, true} {
					step := fixture.Exec("INSERT INTO "+name+" VALUES(1,-100),(2,0),(3,NULL)", 3)
					step.Prepared = prepared
					run(step)
					run(fixture.Select("SELECT id,a FROM "+name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-100)}, {int64(2), int64(0)}, {int64(3), nil}}))
					run(fixture.Exec("DELETE FROM "+name, 3))
				}
			}
		})
	}
}
