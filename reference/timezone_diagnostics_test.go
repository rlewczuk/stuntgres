//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceTimezoneDiagnosticsAndAtomicity(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("UPDATE tz_witness SET z='epoch',q='10:00+00' WHERE id=1", 1))
	sentinel := referenceSelect("SELECT id,z::TEXT AS z,q::TEXT AS q FROM tz_witness ORDER BY id", []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "1970-01-01 00:00:00+00", "10:00:00+00"}})
	for _, tc := range []struct{ typ, input, code, message, hint string }{
		{"TIMESTAMPTZ", "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, ""},
		{"TIMESTAMPTZ", "2004-10-19 10:00+16", "22009", `time zone displacement out of range: "2004-10-19 10:00+16"`, ""},
		{"TIMESTAMPTZ", "2004-10-19 10:00+00:00:60", "22009", `time zone displacement out of range: "2004-10-19 10:00+00:00:60"`, ""},
		{"TIMESTAMPTZ", "2004-10-19 10:00+02:60", "22009", `time zone displacement out of range: "2004-10-19 10:00+02:60"`, ""},
		{"TIMESTAMPTZ", "2004-10-19 10:00 No/Such_Zone", "22023", `time zone "no/such_zone" not recognized`, ""},
		{"TIMETZ", "10:00 America/New_York", "22007", `invalid input syntax for type time with time zone: "10:00 America/New_York"`, ""},
		{"TIMETZ", "10:00+16", "22009", `time zone displacement out of range: "10:00+16"`, ""},
		{"DATE", "31/12/2024", "22008", `date/time field value out of range: "31/12/2024"`, `Perhaps you need a different "DateStyle" setting.`},
		{"TIMESTAMPTZ", "2024-02-30 10:00+00", "22008", `date/time field value out of range: "2024-02-30 10:00+00"`, ""},
		{"TIMESTAMPTZ", "2004-01-01 UTC America/New_York", "22007", `invalid input syntax for type timestamp with time zone: "2004-01-01 UTC America/New_York"`, ""},
	} {
		t.Run(tc.typ+tc.input, func(t *testing.T) {
			q := "/*é*/ SELECT " + tc.typ + " '" + tc.input + "' FROM tz_witness"
			pos := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
			s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: pos})
			s.selectRows = true
			s.errorStage = "execute"
			p.step(t, s)
			s.errorStage = "prepare"
			p.step(t, referencePrepared(s))
			column := "z"
			if tc.typ == "TIMETZ" {
				column = "q"
			}
			if tc.typ == "DATE" {
				column = "d"
			}
			q = "INSERT INTO tz_witness(id," + column + ") VALUES(2,NULL),(3,'" + tc.input + "')"
			pos = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'"+tc.input)]) + 1)
			s = referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: pos})
			s.errorStage = "execute"
			p.step(t, s)
			s.errorStage = "prepare"
			p.step(t, referencePrepared(s))
			timezoneBoth(t, p, sentinel)
			q = "INSERT INTO tz_witness(id," + column + ") VALUES(2,NULL),(3,$1)"
			s = referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}, tc.input)
			pg := *s.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			s.postgresDiagnostic = &pg
			s.errorStage = "execute"
			timezoneBoth(t, p, s)
			timezoneBoth(t, p, sentinel)
		})
	}
	for _, tc := range []struct {
		query, code, message, detail, position string
		parse                                  bool
	}{
		{"SET TIME ZONE 'No/Such_Zone'", "22023", `invalid value for parameter "TimeZone": "No/Such_Zone"`, "", "", false},
		{"SET DateStyle='ISO,SQL'", "22023", `invalid value for parameter "DateStyle": "ISO,SQL"`, `Conflicting "DateStyle" specifications.`, "", false},
		{"SET DateStyle='MDY,DMY'", "22023", `invalid value for parameter "DateStyle": "MDY,DMY"`, `Conflicting "DateStyle" specifications.`, "", false},
		{"SET DateStyle='nonsense'", "22023", `invalid value for parameter "DateStyle": "nonsense"`, `Unrecognized key word: "nonsense".`, "", false},
		{"SET DateStyle='ISO,,MDY'", "22023", `invalid value for parameter "DateStyle": "ISO,,MDY"`, `List syntax is invalid.`, "", false},
		{"SET TIME ZONE 168", "22023", `invalid value for parameter "TimeZone": "168"`, `UTC timezone offset is out of range.`, "", false},
		{"SET TIME ZONE INTERVAL '168 hours'", "22023", `invalid value for parameter "TimeZone": "INTERVAL '168:00:00'"`, `UTC timezone offset is out of range.`, "", false},
		{"SET TIME ZONE INTERVAL '1 month'", "22023", `invalid value for parameter "TimeZone": "INTERVAL '1 mon'"`, `Cannot specify months in time zone interval.`, "", false},
		{"SET TIME ZONE INTERVAL '1 month 1 day'", "22023", `invalid value for parameter "TimeZone": "INTERVAL '1 mon 1 day'"`, `Cannot specify months in time zone interval.`, "", false},
		{"SET TIME ZONE INTERVAL '1 day'", "22023", `invalid value for parameter "TimeZone": "INTERVAL '1 day'"`, `Cannot specify days in time zone interval.`, "", false},
		{"SET TIME ZONE INTERVAL 'nonsense'", "22007", `invalid input syntax for type interval: "nonsense"`, "", "", false},
		{"SET TIME ZONE INTERVAL '1' DAY", "42601", `time zone interval must be HOUR or HOUR TO MINUTE`, "", "28", true},
		{"SET TIME ZONE = 'UTC'", "42601", `syntax error at or near "="`, "", "15", true},
		{"SET TIME ZONE INTERVAL (-1) '1 second'", "42601", `syntax error at or near "-"`, "", "25", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			// Oracle normalization is explicit; this does not ask stuntgres to expose
			// the unrelated general IntervalStyle setting/type feature.
			if _, err := p.postgres.ExecContext(p.ctx, "SET IntervalStyle='postgres'"); err != nil {
				t.Fatal(err)
			}
			s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Position: tc.position})
			s.errorStage = "execute"
			p.step(t, s)
			if tc.parse {
				s.errorStage = "prepare"
			}
			p.step(t, referencePrepared(s))
			timezoneBoth(t, p, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
			timezoneBoth(t, p, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
			timezoneBoth(t, p, sentinel)
		})
	}
	q := "SELECT TIMESTAMP '2004-10-19 10:00' AT TIME ZONE 'No/Such_Zone' FROM tz_witness"
	s := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
	s.selectRows = true
	s.errorStage = "execute"
	timezoneBoth(t, p, s)
	s = referenceImplicitError("UPDATE tz_witness SET z=$1::TEXT::TIMESTAMPTZ WHERE id=1", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`}, "nonsense")
	s.errorStage = "execute"
	timezoneBoth(t, p, s)
	timezoneBoth(t, p, sentinel)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		tx, err := backend.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, tx, backend.record, referenceExec("SET LOCAL TIME ZONE 'America/New_York'", 0))
		s = referenceImplicitError("SET DateStyle='ISO,SQL'", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "DateStyle": "ISO,SQL"`, Detail: `Conflicting "DateStyle" specifications.`})
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, tx, backend.record, s)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if _, err := backend.db.ExecContext(ctx, "UPDATE tz_witness SET z='infinity'"); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel must remain ordinary", err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.record, sentinel)
	}
}

func TestReferenceTimezonePrecisionAndLocalNotices(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		var notices []referenceDiagnostic
		_, conn, record := noticeReferencePool(t, p, postgres, &notices)
		for _, prepared := range []bool{false, true} {
			s := referenceExec("SET TIME ZONE INTERVAL (7) '1 second'", 0)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, conn, record, s)
			want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(7) precision reduced to maximum allowed, 6"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("interval precision notices %#v want %#v", notices, want)
			}
			notices = nil
			s = referenceExec("SET LOCAL TIME ZONE 'UTC'", 0)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, conn, record, s)
			want = []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("local notices %#v want %#v", notices, want)
			}
			notices = nil
			referenceRunStep(t, p.ctx, conn, record, referenceSelect("SHOW TIME ZONE", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"<+00:00:01>-00:00:01"}}))
		}
		conn.Close()
	}
}
