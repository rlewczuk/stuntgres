package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var deparseReviewErrorConsumers = []struct {
	name, columns, input, compact, pretty, good, bad, assignment, code, message, selectSQL string
	rows                                                                                   [][]any
	types                                                                                  []string
}{
	{"case_branch", "id INT,a INT,flag BOOL", "CASE WHEN flag THEN 1 ELSE 1/a END>0", "(\nCASE\n    WHEN flag THEN 1\n    ELSE (1 / a)\nEND > 0)", "\nCASE\n    WHEN flag THEN 1\n    ELSE 1 / a\nEND > 0", "(1,0,true),(2,1,false),(3,NULL,false)", "0,false", "flag=false", "22012", "division by zero", "id,a,flag", [][]any{{int64(1), int64(0), true}, {int64(2), int64(1), false}, {int64(3), nil, false}}, []string{"INT4", "INT4", "BOOL"}},
	{"zone_lookup", "id INT,v TIMESTAMP,z TEXT", "(v AT TIME ZONE z)>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "((v AT TIME ZONE z) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(v AT TIME ZONE z) > '2026-10-08 00:00:00+00'::timestamp with time zone", "(1,'2026-10-08 01:00:00','UTC'),(2,NULL,'No/Such_Zone'),(3,'2026-10-08 01:00:00',NULL)", "'2026-10-08 01:00:00','No/Such_Zone'", "z='No/Such_Zone'", "22023", "time zone \"No/Such_Zone\" not recognized", "id,v,z", nil, []string{"INT4", "TIMESTAMP", "TEXT"}},
}

func TestReferenceDeparseConstraintsReviewProviderErrorConsumers(t *testing.T) {
	for _, tc := range deparseReviewErrorConsumers {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("SET TIME ZONE 'UTC'", 0))
				run(referenceExec("CREATE TABLE original("+tc.columns+",CONSTRAINT c CHECK("+tc.input+"))", 0))
				for mode, expr := range []string{tc.compact, tc.pretty} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).rows[0][0].(string)
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
								deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name+" WHERE id=1", "TIMESTAMP", "2026-10-08 01:00:00")
								deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name+" WHERE id=3", "TIMESTAMP", "2026-10-08 01:00:00")
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
		})
	}
}
