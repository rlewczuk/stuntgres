package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewProviderErrorConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewErrorConsumers {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					db := b.db
					db.SetMaxOpenConns(1)
					postgres := b.pg
					run := func(s referenceStep) referenceObserved {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							previous := s.checkMetadata
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
								deparseReviewMetadata(t, b.pg, rows)
								deparseReviewNullableColumns(t, b.pg, rows, "a", "flag")
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return referenceRunStep(t, p.ctx, db, b.recorder, s)
					}
					run(referenceExec("SET TIME ZONE 'UTC'", 0))
					run(referenceExec("CREATE TABLE original("+tc.columns+",CONSTRAINT c CHECK("+tc.input+"))", 0))
					for mode, expr := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
						step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
						var actual string
						if err := db.QueryRow(step.query, step.args...).Scan(&actual); err != nil || actual != step.rows[0][0] {
							t.Fatal(actual, step.rows, err)
						}
						run(step)
						run(referencePrepared(step))
						run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(%s,CONSTRAINT c%d %s)", mode, tc.columns, mode, actual), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "c"
						if i > 0 {
							constraint = fmt.Sprintf("c%d", i-1)
						}
						for _, prepared := range []bool{false, true} {
							step := referenceExec("INSERT INTO "+name+" VALUES"+tc.good, 3)
							step.prepared = prepared
							run(step)
							snapshot := func() {
								if tc.rows != nil {
									run(referenceSelect("SELECT "+tc.selectSQL+" FROM "+name+" ORDER BY id", strings.Split(tc.selectSQL, ","), tc.types, tc.rows))
								} else {
									run(referenceSelect("SELECT id,z FROM "+name+" ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "UTC"}, {int64(2), "No/Such_Zone"}, {int64(3), nil}}))
									deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=1", "TIMESTAMP", "2026-10-08 01:00:00")
									deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=3", "TIMESTAMP", "2026-10-08 01:00:00")
									run(referenceSelect("SELECT v FROM "+name+" WHERE id=2", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{nil}}))
								}
							}
							snapshot()
							goodFirst := strings.Split(tc.good, ")")[0] + ")"
							for _, query := range []string{
								"INSERT INTO " + name + " VALUES(9," + tc.bad + ")",
								"INSERT INTO " + name + " VALUES" + strings.Replace(goodFirst, "(1,", "(8,", 1) + ",(9," + tc.bad + ")," + strings.Replace(goodFirst, "(1,", "(10,", 1),
								"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1",
							} {
								step := referenceImplicitError(query, referenceDiagnostic{Code: tc.code, Message: tc.message})
								step.prepared = prepared
								run(step)
								snapshot()
							}
							if tc.name == "zone_lookup" {
								step := referenceImplicitError("INSERT INTO "+name+" VALUES(9,'2026-10-07 23:00:00','UTC')", referenceCheckViolation(name, constraint, "9, 2026-10-07 23:00:00, UTC"))
								step.prepared = prepared
								run(step)
								snapshot()
							}
							run(referenceExec("DELETE FROM "+name, 3))
						}
					}
				})
			}
		})
	}
}
