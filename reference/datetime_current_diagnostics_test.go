package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceDatetimeCurrentNoticePhases(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			datetimeCurrentSeed(t, ctx, conn)
			forms := []struct{ name, typ string }{{"CURRENT_TIME", "TIME(%d) WITH TIME ZONE"}, {"CURRENT_TIMESTAMP", "TIMESTAMP(%d) WITH TIME ZONE"}, {"LOCALTIME", "TIME(%d)"}, {"LOCALTIMESTAMP", "TIMESTAMP(%d)"}}
			assertNotices := func(want []referenceDiagnostic, phase string) {
				t.Helper()
				if !reflect.DeepEqual(notices, want) {
					t.Errorf("%s complete notices=%#v want=%#v", phase, notices, want)
				}
				notices = nil
			}
			for _, form := range forms {
				for _, precision := range []int{7, 2147483647} {
					t.Run(form.name+"/"+strconv.Itoa(precision), func(t *testing.T) {
						notices = nil
						q := "SELECT " + form.name + "(" + strconv.Itoa(precision) + ")=" + form.name + "(6) AS same"
						want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: fmt.Sprintf(form.typ, precision) + " precision reduced to maximum allowed, 6"}}
						datetimeCurrentExec(t, ctx, conn, "BEGIN", 0)
						defer conn.ExecContext(ctx, "ROLLBACK")
						s := referenceSelect(q, []string{"same"}, []string{"BOOL"}, [][]any{{true}})
						referenceRunStep(t, ctx, conn, record, s)
						assertNotices(want, "direct Query")
						datetimeCurrentExec(t, ctx, conn, q, 1)
						assertNotices(want, "direct Exec")
						stmt, err := conn.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						assertNotices(want, "Prepare")
						held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
						referenceRunStep(t, ctx, held, record, s)
						assertNotices(nil, "held Execute A")
						datetimeCurrentExec(t, ctx, conn, "COMMIT", 0)
						datetimeCurrentExec(t, ctx, conn, "BEGIN", 0)
						referenceRunStep(t, ctx, held, record, s)
						assertNotices(nil, "held Execute B")
						datetimeCurrentExec(t, ctx, conn, "SET LOCAL client_min_messages TO error", 0)
						referenceRunStep(t, ctx, conn, record, s)
						assertNotices(nil, "suppressed direct warning")
						datetimeCurrentExec(t, ctx, conn, "SET LOCAL client_min_messages TO notice", 0)
						referenceRunStep(t, ctx, conn, record, s)
						assertNotices(want, "restored direct warning")
						datetimeCurrentExec(t, ctx, conn, "COMMIT", 0)
					})
				}
			}
		})
	}
}

func TestReferenceDatetimeCurrentDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			suffixes := []struct {
				sql, token string
				offset     int
			}{{"(-1)", "-", 1}, {"(+1)", "+", 1}, {"()", ")", 1}, {"($1)", "$1", 1}, {"(NULL)", "NULL", 1}, {"(1.5)", "1.5", 1}, {"(1e2)", "1e2", 1}, {"(1+1)", "+", 2}, {"(1,2)", ",", 2}, {"(2147483648)", "2147483648", 1}}
			check := func(t *testing.T, q, token string, position int) {
				t.Helper()
				want := controlsReferenceDiagnostic("42601", `syntax error at or near "`+token+`"`)
				want.Position = strconv.Itoa(position)
				// Independent route subtests expose complete records and failure stages.
				for _, route := range []string{"Query", "Exec", "Prepare"} {
					t.Run(route, func(t *testing.T) {
						s := referenceErrorDiagnostic(q, want)
						s.errorStage = "execute"
						if route == "Query" {
							s.selectRows = true
						}
						if route == "Prepare" {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, conn, backend.recorder, s)
					})
				}
			}
			for _, name := range []string{"CURRENT_TIME", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP"} {
				for _, suffix := range suffixes {
					q := "SELECT " + name + suffix.sql
					position := utf8.RuneCountInString("SELECT "+name) + suffix.offset + 1
					t.Run(name+suffix.sql, func(t *testing.T) { check(t, q, suffix.token, position) })
				}
			}
			for _, suffix := range []string{"()", "(1)"} {
				t.Run("CURRENT_DATE"+suffix, func(t *testing.T) { check(t, "SELECT CURRENT_DATE"+suffix, "(", 20) })
			}
			for _, prefix := range []string{"/*é🙂*/ ", " \n\t"} {
				q := prefix + "SELECT CURRENT_TIMESTAMP(-1)"
				t.Run("unicode/"+strconv.Itoa(len(prefix)), func(t *testing.T) { check(t, q, "-", utf8.RuneCountInString(prefix)+26) })
			}
			for _, q := range []string{`SELECT "current_timestamp"() FROM dc_anchor`, `SELECT pg_catalog."current_timestamp"() FROM dc_anchor`} {
				t.Run(q, func(t *testing.T) {
					name := "current_timestamp"
					if strings.Contains(q, "pg_catalog") {
						name = "pg_catalog.current_timestamp"
					}
					want := controlsReferenceDiagnostic("42883", "function "+name+"() does not exist")
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					want.Position = "8"
					for _, route := range []string{"Query", "Exec", "Prepare"} {
						t.Run(route, func(t *testing.T) {
							s := referenceErrorDiagnostic(q, want)
							s.errorStage = "execute"
							if route == "Query" {
								s.selectRows = true
							}
							if route == "Prepare" {
								s = referencePrepared(s)
								s.errorStage = "prepare"
							}
							referenceRunStep(t, p.ctx, conn, backend.recorder, s)
						})
					}
				})
			}
			t.Run("atomicity", func(t *testing.T) {
				datetimeCurrentExec(t, p.ctx, conn, "CREATE TABLE dc_atomic(n INTEGER,z TIMESTAMPTZ)", 0)
				datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO dc_atomic VALUES(9,TIMESTAMPTZ '2000-01-01+00')", 1)
				q := "INSERT INTO dc_atomic VALUES(1,CURRENT_TIMESTAMP),(2,CURRENT_TIMESTAMP(-1))"
				want := controlsReferenceDiagnostic("42601", `syntax error at or near "-"`)
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "-1")]) + 1)
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceErrorDiagnostic(q, want))
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT n,z::TEXT AS z FROM dc_atomic", []string{"n", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "2000-01-01 00:00:00+00"}}))
				datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
				defer conn.ExecContext(p.ctx, "ROLLBACK")
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceErrorDiagnostic(q, want))
				aborted := controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceErrorDiagnostic("SELECT "+datetimeCurrentProjection, aborted))
				datetimeCurrentExec(t, p.ctx, conn, "ROLLBACK", 0)
				datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
				stamp := datetimeCurrentWitness(t, p.ctx, conn)
				datetimeCurrentCheck(t, p.ctx, conn, backend.pg, "SELECT "+datetimeCurrentProjection, stamp, 0, 6, 1)
			})
		})
	}
}
