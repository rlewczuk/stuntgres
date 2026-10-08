//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceDateDecodedCivilInputs(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *referenceRecorder
	}{
		{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord},
	} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			run(referenceExec("INSERT INTO d VALUES('2000-01-01')", 1))
			for _, tc := range []struct {
				input, canonical string
				year             int
				month            time.Month
				day              int
			}{
				{"2001--01-01", "2001-01-01", 2001, 1, 1},
				{"1999-01-08 23:59:59-08", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 24:00", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 250000", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 049900", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 040599", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 23:59:60", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 23:59:59.9999999", "1999-01-08", 1999, 1, 8},
				{"J2451187.5", "1999-01-08", 1999, 1, 8},
				{"1999.366", "2000-01-01", 2000, 1, 1}, {"2000.366", "2000-12-31", 2000, 12, 31},
				{"J0", "4714-11-24 BC", -4713, 11, 24},
				{"1999-01-08 04:05 America/New_York", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 04:05:06.1e2", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 04:05 X0", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 04:05 X0Y0", "1999-01-08", 1999, 1, 8},
				{"1999-01-08 04:05 X0Y", "1999-01-08", 1999, 1, 8},
				{" EPOCH ", "1970-01-01", 1970, 1, 1},
				{" InFiNiTy ", "infinity", 0, 0, 0}, {" -INFINITY ", "-infinity", 0, 0, 0},
				{"January 8, 99 BC", "0099-01-08 BC", -98, 1, 8}, {"01/02/03", "2003-01-02", 2003, 1, 2},
				{"Jan 8 69", "2069-01-08", 2069, 1, 8}, {"Jan 8 70", "1970-01-08", 1970, 1, 8},
			} {
				var value any = tc.canonical
				if b.postgres {
					if tc.month != 0 {
						value = time.Date(tc.year, tc.month, tc.day, 0, 0, 0, 0, time.UTC)
					}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceSelect("SELECT DATE '"+tc.input+"' FROM d", []string{"date"}, []string{"DATE"}, [][]any{{value}})
					s.prepared = prepared
					run(s)
					// Each isolated destination supplies contextual type for both text
					// constants and execution-owned wire parameters.
					run(referenceExec("CREATE TABLE inputs(v DATE)", 0))
					s = referenceExec("INSERT INTO inputs VALUES($1)", 1, tc.input)
					s.prepared = prepared
					run(s)
					s = referenceExec("INSERT INTO inputs VALUES('"+tc.input+"')", 1)
					s.prepared = prepared
					run(s)
					run(referenceSelect("SELECT v FROM inputs", []string{"v"}, []string{"DATE"}, [][]any{{value}, {value}}))
					run(referenceExec("DROP TABLE inputs", 0))
				}
			}
			run(referenceExec(`CREATE TABLE date(date "date")`, 0))
			run(referenceExec("INSERT INTO date VALUES('1999-01-08')", 1))
			var canonical any = "1999-01-08"
			if b.postgres {
				canonical = time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect(`SELECT "date" '1999-01-08' FROM date`, []string{"date"}, []string{"DATE"}, [][]any{{canonical}})
				s.prepared = prepared
				run(s)
				s = referenceSelect("SELECT date FROM date", []string{"date"}, []string{"DATE"}, [][]any{{canonical}})
				s.prepared = prepared
				run(s)
			}
		})
	}
}

func TestReferenceDateDecodeDiagnosticsAndModifierPhases(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *referenceRecorder
	}{
		{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord},
	} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary string }{
				{"1999-01-08 25:00", "22008", "date/time field value out of range"},
				{"1999-01-08 24:00:00.1", "22008", "date/time field value out of range"},
				{"J2451187.5 J2451187.5", "22007", "invalid input syntax for type date"},
				{"J2451187.5 1999-01-08", "22007", "invalid input syntax for type date"},
				{"J-1", "22007", "invalid input syntax for type date"},
				{"J2147483647", "22008", "date out of range"},
				{"999999999999999999-01-01", "22008", "date/time field value out of range"},
				{"1999-01-08 04:05 UTC UTC", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05 EDT DST", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05:06 now1", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05:06 now+2", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05+16", "22009", "time zone displacement out of range"},
				{"1999-01-08 04:05+01:99", "22009", "time zone displacement out of range"},
				{"2001-02-29 25:00", "22008", "date/time field value out of range"},
				{"5874898-01-01 25:00", "22008", "date/time field value out of range"},
				{"2001-13-01 04:05+16", "22009", "time zone displacement out of range"},
				{"1999-01-08 04:05 America/Bogus", "22023", ""},
			} {
				message := tc.primary + ": \"" + tc.input + "\""
				if tc.primary == "" {
					message = `time zone "america/bogus" not recognized`
				}
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Code: tc.code, Message: message, Position: "13"}
					s := referenceImplicitError("SELECT DATE '"+tc.input+"' FROM d", want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					want.Position = ""
					want.Context = "unnamed portal parameter $1 = '...'"
					s = referenceImplicitError("INSERT INTO d VALUES($1)", want)
					s.args = []any{tc.input}
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
			for _, typ := range []string{"date(0)", "date(3)", `"date"(3)`} {
				want := referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20"}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("CREATE TABLE bad(v "+typ+")", want)
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
				run(referenceExec("CREATE TABLE bad(v DATE)", 0))
				run(referenceExec("DROP TABLE bad", 0))
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT DATE(3) '1999-01-08' FROM d", referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8"})
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
			}
		})
	}
}
