package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceTimeReviewClockPrecedence(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIME)", 0))
			run(referenceExec("INSERT INTO t VALUES('00:00')", 1))
			// Source-derived expectations: DecodeTimeOnly token loop precedes
			// ValidateDate/AMPM/domain. A leading DATE token is not a zone here.
			for _, tc := range []struct{ input, code, message, hint string }{
				{"25:00 UTC UTC", "22007", "invalid input syntax for type time: \"25:00 UTC UTC\"", ""},
				{"UTC UTC 25:00", "22007", "invalid input syntax for type time: \"UTC UTC 25:00\"", ""},
				{"24:00:00.1 America/Bogus", "22023", "time zone \"america/bogus\" not recognized", ""},
				{"America/Bogus 24:00:00.1", "22007", "invalid input syntax for type time: \"America/Bogus 24:00:00.1\"", ""},
				{"2003-13-07 25:00", "22008", "date/time field value out of range: \"2003-13-07 25:00\"", "Perhaps you need a different \"DateStyle\" setting."},
				{"25:00 2003-13-07", "22007", "invalid input syntax for type time: \"25:00 2003-13-07\"", ""},
				{"25:00 AM PM", "22007", "invalid input syntax for type time: \"25:00 AM PM\"", ""},
				{"AM PM 25:00", "22007", "invalid input syntax for type time: \"AM PM 25:00\"", ""},
				{"12:60 America/Bogus", "22008", "date/time field value out of range: \"12:60 America/Bogus\"", ""},
				{"America/Bogus 12:60", "22007", "invalid input syntax for type time: \"America/Bogus 12:60\"", ""},
			} {
				t.Run(tc.input, func(t *testing.T) {
					run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					for _, later := range []bool{false, true} {
						q := "INSERT INTO t VALUES('" + tc.input + "')"
						parameter := "INSERT INTO t VALUES($1)"
						if later {
							q = "INSERT INTO t VALUES('24:00'),('" + tc.input + "')"
							parameter = "INSERT INTO t VALUES('24:00'),($1)"
						}
						want := referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: strconv.Itoa(strings.Index(q, "'"+tc.input) + 1)}
						for _, prepared := range []bool{false, true} {
							s := referenceImplicitError(q, want)
							s.prepared = prepared
							s.errorStage = "execute"
							if prepared {
								s.errorStage = "prepare"
							}
							run(s)
							wantParameter := want
							wantParameter.Position = ""
							s = referenceImplicitError(parameter, wantParameter, tc.input)
							s.prepared, s.errorStage = prepared, "execute"
							pg := *s.diagnostic
							pg.Context = "unnamed portal parameter $1 = '...'"
							s.diagnostic = &pg
							run(s)
							run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"00:00:00"}})))
						}
					}
				})
			}
		})
	}
}

func TestReferenceTimeReviewAssignmentIdentity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, declaration, destination, literal, source, sentinel, databaseType string
				value                                                                   any
			}{
				{"ints", "INTEGER", "integer", "TIME '04:05'", "time without time zone", "1", "INT4", int64(1)},
				{"stamps", "TIMESTAMP", "timestamp without time zone", "TIME '04:05'", "time without time zone", "'2003-07-07'", "TIMESTAMP", time.Date(2003, 7, 7, 0, 0, 0, 0, time.UTC)},
				{"times", "TIME", "time without time zone", "TIMESTAMP '2003-07-07'", "timestamp without time zone", "'00:00'", "TIME", "00:00:00"},
				{"stampints", "INTEGER", "integer", "TIMESTAMP '2003-07-07'", "timestamp without time zone", "1", "INT4", int64(1)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE "+tc.name+"(v "+tc.declaration+")", 0))
					run(referenceExec("INSERT INTO "+tc.name+" VALUES("+tc.sentinel+")", 1))
					count := 1
					for _, later := range []bool{false, true} {
						q := "INSERT INTO " + tc.name + " VALUES("
						if later {
							q += tc.sentinel + "),("
						}
						q += tc.literal + ")"
						// parse_target.c uses exprLocation(Const): gram.y/parse_coerce.c
						// preserve the quoted literal location, not the type keyword.
						want := referenceDiagnostic{Code: "42804", Message: "column \"v\" is of type " + tc.destination + " but expression is of type " + tc.source, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(q, tc.literal) + strings.Index(tc.literal, "'") + 1)}
						for _, prepared := range []bool{false, true} {
							if tc.name == "times" {
								// Both backends permit TIMESTAMP->TIME assignment.
								affected := int64(1)
								if later {
									affected = 2
								}
								s := referenceExec(q, affected)
								s.prepared = prepared
								run(s)
								count += int(affected)
							} else {
								s := referenceImplicitError(q, want)
								s.prepared, s.errorStage = prepared, "execute"
								if prepared {
									s.errorStage = "prepare"
								}
								run(s)
							}
							rows := [][]any{{tc.value}}
							if tc.name == "times" {
								rows = make([][]any, count)
								for i := range rows {
									rows[i] = []any{"00:00:00"}
								}
							}
							if tc.databaseType == "TIME" {
								rows = referenceTimeRows(t, backend.pg, []string{"TIME"}, rows)
							}
							run(referenceSelect("SELECT v FROM "+tc.name, []string{"v"}, []string{tc.databaseType}, rows))
						}
					}
				})
			}
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE nonmidnight(v TIME)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceExec("INSERT INTO nonmidnight VALUES(TIMESTAMP '2003-07-07 04:05:06')", 1)
				s.prepared = prepared
				run(s)
			}
			run(referenceSelect("SELECT v FROM nonmidnight", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:06"}})))
		})
	}
}
