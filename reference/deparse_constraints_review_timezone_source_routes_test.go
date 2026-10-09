package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewTimezoneConsumersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewZoneCases {
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
								deparseReviewNullableColumns(t, b.pg, rows, "id", "v")
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return referenceRunStep(t, p.ctx, db, b.recorder, s)
					}
					run(referenceExec("SET TIME ZONE 'UTC'", 0))
					run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
					run(referenceExec("CREATE TABLE original(id INT,v "+tc.decl+",CONSTRAINT c CHECK("+tc.input+"))", 0))
					for mode, expr := range []string{tc.compact, tc.pretty} {
						q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d,pg_get_expr(conbin,conrelid,%t) AS e FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1, mode == 1)
						step := referenceSelect(q, []string{"d", "e"}, []string{"TEXT", "TEXT"}, [][]any{{"CHECK (" + expr + ")", expr}})
						var actual string
						if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)).Scan(&actual); err != nil || actual != step.rows[0][0] {
							t.Fatal(actual, step.rows, err)
						}
						run(step)
						run(referencePrepared(step))
						run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,v %s,CONSTRAINT c%d %s)", mode, tc.decl, mode, actual), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "c"
						if i > 0 {
							constraint = fmt.Sprintf("c%d", i-1)
						}
						held, err := db.Prepare("INSERT INTO " + name + " VALUES($1,$2)")
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						for _, prepared := range []bool{false, true} {
							run(referenceExec("SET TIME ZONE 'UTC'", 0))
							step := referenceExec("INSERT INTO "+name+" VALUES(1,'"+tc.good1+"'),(2,'"+tc.good2+"'),(3,NULL)", 3)
							step.prepared = prepared
							run(step)
							retainedIDs := [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
							snapshot := func() {
								run(referenceSelect("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, retainedIDs))
								deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=1", tc.decl, tc.good1)
								deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=2", tc.decl, tc.good2)
								run(referenceSelect("SELECT id,v FROM "+name+" WHERE id=3", []string{"id", "v"}, []string{"INT4", tc.decl}, [][]any{{int64(3), nil}}))
							}
							snapshot()
							conversion := strings.Split(tc.input, ")")[0] + ")"
							// The persisted predicate's typed conversion itself has an independent value witness.
							deparseReviewSourceTypedValue(t, db, postgres, "SELECT "+conversion+" AS v FROM "+name+" WHERE id=1", tc.resultDecl, tc.result)
							for _, op := range []struct{ query, id string }{
								{"INSERT INTO " + name + " VALUES(9,'" + tc.bad + "')", "9"},
								{"INSERT INTO " + name + " VALUES(8,'" + tc.good1 + "'),(9,'" + tc.bad + "'),(10,'" + tc.good2 + "')", "9"},
								{"UPDATE " + name + " SET v='" + tc.bad + "' WHERE id=1", "1"},
							} {
								step := referenceImplicitError(op.query, referenceCheckViolation(name, constraint, op.id+", "+tc.badOutput))
								step.prepared = prepared
								run(step)
								snapshot()
							}
							result, err := held.Exec(7, tc.good1)
							if err != nil {
								t.Fatal(err)
							}
							if n, err := result.RowsAffected(); err != nil || n != 1 {
								t.Fatal(n, err)
							}
							// Use the independently valid UTC value before testing the changed-zone value.
							// The SAME plain handle must observe both session states.
							run(referenceExec("DELETE FROM "+name+" WHERE id=7", 1))
							run(referenceExec("SET TIME ZONE 'UTC-2'", 0))
							// A held prepared DML handle observes the new session, not its prepare-time zone.
							_, err = held.Exec(7, tc.changedInput)
							if tc.changedAccept {
								if err != nil {
									t.Fatal(err)
								}
								retainedIDs = append(retainedIDs, []any{int64(7)})
							} else {
								detail := "7, " + tc.changedOutput
								want := referenceCheckViolation(name, constraint, detail)
								want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
								referenceAssertDiagnostic(t, err, &want)
							}
							changedResult := tc.result
							switch tc.name {
							case "stamp_fixed", "stamp_local":
								changedResult = "2026-10-07 23:00:00+00"
							case "zstamp_fixed":
								changedResult = "2026-10-08 03:00:00"
							case "zstamp_local":
								changedResult = "2026-10-08 05:00:00"
							case "timetz_fixed", "timetz_local":
								changedResult = "12:00:00+02"
							}
							deparseReviewSourceTypedValue(t, db, postgres, "SELECT "+conversion+" AS v FROM "+name+" WHERE id=1", tc.resultDecl, changedResult)
							if tc.resultDecl == "TIMESTAMPTZ" {
								var instant time.Time
								want, err := time.Parse("2006-01-02 15:04:05Z07", changedResult)
								if err != nil {
									t.Fatal(err)
								}
								if err := db.QueryRow("SELECT " + conversion + " FROM " + name + " WHERE id=1").Scan(&instant); err != nil || !instant.Equal(want) {
									t.Fatal(instant, want, err)
								}
							}
							if tc.name == "timetz_local" {
								deparseReviewSourceTypedValue(t, db, postgres, "SELECT v AT LOCAL AS v FROM "+name+" WHERE id=1", "TIMETZ", "12:00:00+02")
								run(referenceSelect("SELECT (v AT LOCAL)=TIMETZ '12:00:00+02' AS same FROM "+name+" WHERE id=1", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
							}
							if tc.name == "stamp_local" {
								deparseReviewSourceTypedValue(t, db, postgres, "SELECT v AT LOCAL AS v FROM "+name+" WHERE id=1", "TIMESTAMPTZ", "2026-10-07 23:00:00+00")
							}
							run(referenceExec("SET TIME ZONE 'UTC'", 0))
							snapshot()
							if tc.changedAccept {
								deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name+" WHERE id=7", tc.decl, tc.changedInput)
							}
							deleted := int64(3)
							if tc.changedAccept {
								deleted++
							}
							run(referenceExec("DELETE FROM "+name, deleted))
						}
						held.Close()
					}
				})
			}
		})
	}
}
