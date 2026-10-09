package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var deparseReviewZoneCases = []struct {
	name, decl, input, compact, pretty, good1, good2, bad, badOutput, resultDecl, result, changedInput, changedOutput string
	changedAccept                                                                                                     bool
}{
	{"stamp_fixed", "TIMESTAMP", "(v AT TIME ZONE 'UTC')>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "((v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone", "2026-10-08 01:00:00", "2026-10-08 03:00:00", "2026-10-07 23:00:00", "2026-10-07 23:00:00", "TIMESTAMPTZ", "2026-10-08 01:00:00+00", "2026-10-08 01:00:00", "2026-10-08 01:00:00", false},
	{"zstamp_fixed", "TIMESTAMPTZ", "(v AT TIME ZONE 'UTC')>TIMESTAMP '2026-10-08 00:00:00'", "((v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00'::timestamp without time zone)", "(v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00'::timestamp without time zone", "2026-10-08 01:00:00+00", "2026-10-08 03:00:00+00", "2026-10-07 23:00:00+00", "2026-10-07 23:00:00+00", "TIMESTAMP", "2026-10-08 01:00:00", "2026-10-08 01:00:00+00", "2026-10-08 03:00:00+02", true},
	{"timetz_fixed", "TIMETZ", "(v AT TIME ZONE 'UTC')=TIMETZ '10:00:00+00'", "((v AT TIME ZONE 'UTC'::text) = '10:00:00+00'::time with time zone)", "(v AT TIME ZONE 'UTC'::text) = '10:00:00+00'::time with time zone", "05:00:00-05", "05:00:00-05", "04:00:00-05", "04:00:00-05", "TIMETZ", "10:00:00+00", "05:00:00-05", "05:00:00-05", false},
	{"stamp_local", "TIMESTAMP", "(v AT LOCAL)>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "((v AT LOCAL) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(v AT LOCAL) > '2026-10-08 00:00:00+00'::timestamp with time zone", "2026-10-08 01:00:00", "2026-10-08 03:00:00", "2026-10-07 23:00:00", "2026-10-07 23:00:00", "TIMESTAMPTZ", "2026-10-08 01:00:00+00", "2026-10-08 01:00:00", "2026-10-08 01:00:00", false},
	{"zstamp_local", "TIMESTAMPTZ", "(v AT LOCAL)>TIMESTAMP '2026-10-08 02:00:00'", "((v AT LOCAL) > '2026-10-08 02:00:00'::timestamp without time zone)", "(v AT LOCAL) > '2026-10-08 02:00:00'::timestamp without time zone", "2026-10-08 03:00:00+00", "2026-10-08 04:00:00+00", "2026-10-08 01:00:00+00", "2026-10-08 01:00:00+00", "TIMESTAMP", "2026-10-08 03:00:00", "2026-10-08 01:00:00+00", "2026-10-08 03:00:00+02", true},
	{"timetz_local", "TIMETZ", "(v AT LOCAL)=TIMETZ '10:00:00+00'", "((v AT LOCAL) = '10:00:00+00'::time with time zone)", "(v AT LOCAL) = '10:00:00+00'::time with time zone", "10:00:00+00", "10:00:00+00", "09:00:00+00", "09:00:00+00", "TIMETZ", "10:00:00+00", "10:00:00+00", "10:00:00+00", false},
	{"etc_stamp", "TIMESTAMP", "(v AT TIME ZONE 'Etc/UTC')>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "((v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone", "2026-10-08 01:00:00", "2026-10-08 03:00:00", "2026-10-07 23:00:00", "2026-10-07 23:00:00", "TIMESTAMPTZ", "2026-10-08 01:00:00+00", "2026-10-08 01:00:00", "2026-10-08 01:00:00", true},
	{"etc_zstamp", "TIMESTAMPTZ", "(v AT TIME ZONE 'Etc/UTC')>TIMESTAMP '2026-10-08 00:00:00'", "((v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00'::timestamp without time zone)", "(v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00'::timestamp without time zone", "2026-10-08 01:00:00+00", "2026-10-08 03:00:00+00", "2026-10-07 23:00:00+00", "2026-10-07 23:00:00+00", "TIMESTAMP", "2026-10-08 01:00:00", "2026-10-08 01:00:00+00", "2026-10-08 03:00:00+02", true},
	{"etc_timetz", "TIMETZ", "(v AT TIME ZONE 'Etc/UTC')=TIMETZ '10:00:00+00'", "((v AT TIME ZONE 'Etc/UTC'::text) = '10:00:00+00'::time with time zone)", "(v AT TIME ZONE 'Etc/UTC'::text) = '10:00:00+00'::time with time zone", "05:00:00-05", "05:00:00-05", "04:00:00-05", "04:00:00-05", "TIMETZ", "10:00:00+00", "05:00:00-05", "05:00:00-05", true},
}

func TestReferenceDeparseConstraintsReviewTimezoneConsumers(t *testing.T) {
	for _, tc := range deparseReviewZoneCases {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("SET TIME ZONE 'UTC'", 0))
				run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
				run(referenceExec("CREATE TABLE original(id INT,v "+tc.decl+",CONSTRAINT c CHECK("+tc.input+"))", 0))
				for mode, expr := range []string{tc.compact, tc.pretty} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d,pg_get_expr(conbin,conrelid,%t) AS e FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1, mode == 1)
					step := referenceSelect(q, []string{"d", "e"}, []string{"TEXT", "TEXT"}, [][]any{{"CHECK (" + expr + ")", expr}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,v %s,CONSTRAINT c%d %s)", mode, tc.decl, mode, actual), 0))
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "c"
					if i > 0 {
						constraint = fmt.Sprintf("c%d", i-1)
					}
					held, err := conn.PrepareContext(context.Background(), "INSERT INTO "+name+" VALUES($1,$2)")
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
							deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name+" WHERE id=1", tc.decl, tc.good1)
							deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name+" WHERE id=2", tc.decl, tc.good2)
							run(referenceSelect("SELECT id,v FROM "+name+" WHERE id=3", []string{"id", "v"}, []string{"INT4", tc.decl}, [][]any{{int64(3), nil}}))
						}
						snapshot()
						conversion := strings.Split(tc.input, ")")[0] + ")"
						// The persisted predicate's typed conversion itself has an independent value witness.
						deparseReviewTypedValue(t, conn, postgres, "SELECT "+conversion+" AS v FROM "+name+" WHERE id=1", tc.resultDecl, tc.result)
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
						run(referenceExec("SET TIME ZONE 'UTC-2'", 0))
						// A held prepared DML handle observes the new session, not its prepare-time zone.
						_, err = held.ExecContext(context.Background(), 7, tc.changedInput)
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
						deparseReviewTypedValue(t, conn, postgres, "SELECT "+conversion+" AS v FROM "+name+" WHERE id=1", tc.resultDecl, changedResult)
						if tc.name == "timetz_local" {
							deparseReviewTypedValue(t, conn, postgres, "SELECT v AT LOCAL AS v FROM "+name+" WHERE id=1", "TIMETZ", "12:00:00+02")
							run(referenceSelect("SELECT (v AT LOCAL)=TIMETZ '12:00:00+02' AS same FROM "+name+" WHERE id=1", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
						}
						if tc.name == "stamp_local" {
							deparseReviewTypedValue(t, conn, postgres, "SELECT v AT LOCAL AS v FROM "+name+" WHERE id=1", "TIMESTAMPTZ", "2026-10-07 23:00:00+00")
						}
						run(referenceExec("SET TIME ZONE 'UTC'", 0))
						snapshot()
						if tc.changedAccept {
							deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name+" WHERE id=7", tc.decl, tc.changedInput)
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
		})
	}
}
