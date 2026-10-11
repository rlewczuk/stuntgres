package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewProviderErrorConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewErrorConsumers {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					db := b.DB
					db.SetMaxOpenConns(1)
					postgres := b.PG
					run := func(s fixture.Step) fixture.Observed {
						s.NonContext = true
						if s.SelectRows && s.Diagnostic == nil {
							previous := s.CheckMetadata
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
								fixture.DeparseReviewMetadata(t, b.PG, rows)
								deparseReviewNullableColumns(t, b.PG, rows, "a", "flag")
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
					}
					run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
					run(fixture.Exec("CREATE TABLE original("+tc.columns+",CONSTRAINT c CHECK("+tc.input+"))", 0))
					for mode, expr := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
						step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
						var actual string
						if err := db.QueryRow(step.Query, step.Args...).Scan(&actual); err != nil || actual != step.Rows[0][0] {
							t.Fatal(actual, step.Rows, err)
						}
						run(step)
						run(fixture.Prepared(step))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(%s,CONSTRAINT c%d %s)", mode, tc.columns, mode, actual), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "c"
						if i > 0 {
							constraint = fmt.Sprintf("c%d", i-1)
						}
						for _, prepared := range []bool{false, true} {
							step := fixture.Exec("INSERT INTO "+name+" VALUES"+tc.good, 3)
							step.Prepared = prepared
							run(step)
							snapshot := func() {
								if tc.rows != nil {
									run(fixture.Select("SELECT "+tc.selectSQL+" FROM "+name+" ORDER BY id", strings.Split(tc.selectSQL, ","), tc.types, tc.rows))
								} else {
									run(fixture.Select("SELECT id,z FROM "+name+" ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "UTC"}, {int64(2), "No/Such_Zone"}, {int64(3), nil}}))
									deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=1", "TIMESTAMP", "2026-10-08 01:00:00")
									deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=3", "TIMESTAMP", "2026-10-08 01:00:00")
									run(fixture.Select("SELECT v FROM "+name+" WHERE id=2", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{nil}}))
								}
							}
							snapshot()
							goodFirst := strings.Split(tc.good, ")")[0] + ")"
							for _, query := range []string{
								"INSERT INTO " + name + " VALUES(9," + tc.bad + ")",
								"INSERT INTO " + name + " VALUES" + strings.Replace(goodFirst, "(1,", "(8,", 1) + ",(9," + tc.bad + ")," + strings.Replace(goodFirst, "(1,", "(10,", 1),
								"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1",
							} {
								step := fixture.ImplicitError(query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
								step.Prepared = prepared
								run(step)
								snapshot()
							}
							if tc.name == "zone_lookup" {
								step := fixture.ImplicitError("INSERT INTO "+name+" VALUES(9,'2026-10-07 23:00:00','UTC')", fixture.CheckViolation(name, constraint, "9, 2026-10-07 23:00:00, UTC"))
								step.Prepared = prepared
								run(step)
								snapshot()
							}
							run(fixture.Exec("DELETE FROM "+name, 3))
						}
					}
				})
			}
		})
	}
}
