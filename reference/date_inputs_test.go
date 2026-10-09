package reference_test

import (
	"testing"
	"time"
)

func TestReferenceDateDecodedCivilInputs(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
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
				if b.pg {
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
			if b.pg {
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

func TestReferenceDateRepeatedSeparatorsContextualAndBound(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"INSERT INTO d VALUES('2001--01-01')", "INSERT INTO d VALUES($1)"} {
					var args []any
					if query == "INSERT INTO d VALUES($1)" {
						args = []any{"2001--01-01"}
					}
					s := referenceExec(query, 1, args...)
					s.prepared = prepared
					run(s)
				}
			}
			value := referenceDateExpected(b.pg, "2001-01-01")
			run(referenceSelect("SELECT v FROM d WHERE v='2001--01-01'", []string{"v"}, []string{"DATE"}, [][]any{{value}, {value}, {value}, {value}}))
		})
	}
}
