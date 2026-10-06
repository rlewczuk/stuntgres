//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceDateDocumentedInputsAndCivilBounds(t *testing.T) {
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
			run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			forms := []string{"1999-01-08", "January 8, 1999", "1999-Jan-08", "Jan-08-1999", "08-Jan-1999", "19990108", "990108", "1999.008", "J2451187", "1999-01-08"}
			for i, input := range forms {
				s := referenceExec("INSERT INTO d VALUES($1,$2)", 1, i, input)
				s.prepared = i%2 == 0
				run(s)
			}
			var finite any = "1999-01-08"
			if b.postgres {
				finite = time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)
			}
			want := make([][]any, len(forms))
			for i := range want {
				want[i] = []any{finite}
			}
			for _, q := range []string{"SELECT v FROM d ORDER BY id", "SELECT DATE '1999-01-08' FROM d WHERE v='1999-01-08'"} {
				name := "v"
				if q != "SELECT v FROM d ORDER BY id" {
					name = "date"
				}
				s := referenceSelect(q, []string{name}, []string{"DATE"}, want)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE bounds(id INTEGER,v DATE)", 0))
			cases := []struct {
				input, canonical string
				year             int
				month            time.Month
				day              int
			}{
				{"4714-11-24 BC", "4714-11-24 BC", -4713, 11, 24},
				{"5874897-12-31", "5874897-12-31", 5874897, 12, 31},
				{"300000-01-01", "300000-01-01", 300000, 1, 1},
				{"0001-02-29 BC", "0001-02-29 BC", 0, 2, 29},
				{"1600-02-29", "1600-02-29", 1600, 2, 29},
				{"2000-02-29", "2000-02-29", 2000, 2, 29},
				{"2400-02-29", "2400-02-29", 2400, 2, 29},
				{"epoch", "1970-01-01", 1970, 1, 1},
				{"infinity", "infinity", 0, 0, 0}, {"-infinity", "-infinity", 0, 0, 0},
			}
			want = nil
			for i, tc := range cases {
				s := referenceExec("INSERT INTO bounds VALUES($1,$2)", 1, i, tc.input)
				s.prepared = i%2 == 0
				run(s)
				var value any = tc.canonical
				if b.postgres {
					if tc.month != 0 {
						value = time.Date(tc.year, tc.month, tc.day, 0, 0, 0, 0, time.UTC)
					}
				}
				want = append(want, []any{value})
			}
			run(referenceSelect("SELECT v FROM bounds ORDER BY id", []string{"v"}, []string{"DATE"}, want))
		})
	}
}

func TestReferenceDateDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{
		{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord},
	} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary, hint string }{
				{"bad", "22007", "invalid input syntax for type date", ""},
				{"2001-02-29", "22008", "date/time field value out of range", ""},
				{"0000-01-01", "22008", "date/time field value out of range", ""},
				{"2001-13-01", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
				{"4714-11-23 BC", "22008", "date out of range", ""},
				{"5874898-01-01", "22008", "date out of range", ""},
			} {
				want := referenceDiagnostic{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "22"}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO d VALUES('"+tc.input+"')", want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
			run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
		})
	}
}
