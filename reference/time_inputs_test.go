//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTimeAcceptedInputsPrecisionAndOrdering(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			run(referenceExec("CREATE TABLE anchor(id INTEGER)", 0))
			run(referenceExec("INSERT INTO anchor VALUES(1)", 1))
			run(referenceExec("CREATE TABLE inputs(v TIME)", 0))
			for _, tc := range []struct{ input, want string }{
				{"04:05:06.789", "04:05:06.789"}, {"04:05:06", "04:05:06"}, {"04:05", "04:05:00"}, {"040506", "04:05:06"}, {"0405", "04:05:00"}, {"04:05.5", "00:04:05.5"}, {"4:5:6", "04:05:06"},
				{"04:05 AM", "04:05:00"}, {"04:05 pm", "16:05:00"}, {"12:00 AM", "00:00:00"}, {"12:00 PM", "12:00:00"}, {"00:00 AM", "00:00:00"}, {"00:00 PM", "12:00:00"},
				{"04:05:06.789-8", "04:05:06.789"}, {"04:05:06-08:00", "04:05:06"}, {"04:05:06+0730", "04:05:06"}, {"04:05:06+07:30:00", "04:05:06"}, {"02:03 PST", "02:03:00"}, {"11:59 EDT", "11:59:00"},
				{"2003-03-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39", "15:36:39"}, {"2003-07-07 15:36:39 Asia/Kolkata", "15:36:39"},
				{"04:05 Etc/GMT+5", "04:05:00"}, {"04:05 eTc/UtC", "04:05:00"}, {"04:05 Universal", "04:05:00"}, {"04:05 X0", "04:05:00"}, {"04:05 X0Y0", "04:05:00"}, {"2003-07-07 04:05 X0Y", "04:05:00"},
				{"04:05 MET DST", "04:05:00"}, {"04:05 MSK", "04:05:00"}, {"t040506", "04:05:06"}, {"t04:05:06", "04:05:06"}, {"j2451545.5", "12:00:00"}, {"j2451545 04:05", "04:05:00"}, {"04:05 on", "04:05:00"}, {"04:05 AD", "04:05:00"}, {"allballs", "00:00:00"},
				{"23:59:60", "24:00:00"}, {"23:59:59.9999999", "24:00:00"}, {"04:05:60", "04:06:00"}, {"04:05:06.0000005", "04:05:06"}, {"04:05:06.0000015", "04:05:06.000002"},
			} {
				t.Run(tc.input, func(t *testing.T) {
					want := referenceTimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{tc.want}})
					s := referenceSelect("SELECT TIME '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, want)
					run(s)
					run(referencePrepared(s))
					run(referenceExec("INSERT INTO inputs VALUES($1)", 1, tc.input))
					run(referencePrepared(referenceExec("INSERT INTO inputs VALUES($1)", 1, tc.input)))
				})
			}
			for _, tc := range []struct{ decl, input, want string }{
				{"TIME(0)", "04:05:06.499999", "04:05:06"}, {"TIME(0)", "04:05:06.5", "04:05:07"}, {"TIME(3)", "04:05:06.1235", "04:05:06.124"}, {"TIME(3)", "04:05:06.1234999", "04:05:06.124"}, {"TIME(6)", "04:05:06.0000005", "04:05:06"}, {"TIME(6)", "04:05:06.0000015", "04:05:06.000002"}, {"TIME(0)", "23:59:59.5", "24:00:00"},
			} {
				s := referenceSelect("SELECT "+tc.decl+" '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, referenceTimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{tc.want}}))
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE rounding(v TIME(0))", 0))
			run(referenceExec("INSERT INTO rounding VALUES(TIME(3) '04:05:06.4995'),('04:05:06.4995')", 2))
			run(referenceSelect("SELECT v FROM rounding ORDER BY v", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:07"}})))
			run(referenceExec("CREATE TABLE ordering(v TIME)", 0))
			run(referenceExec("INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)", 5))
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{{"SELECT v FROM ordering ORDER BY v", [][]any{{"00:00:00"}, {"04:05:06.1"}, {"04:05:06.2"}, {"24:00:00"}, {nil}}}, {"SELECT v FROM ordering ORDER BY v DESC", [][]any{{nil}, {"24:00:00"}, {"04:05:06.2"}, {"04:05:06.1"}, {"00:00:00"}}}, {"SELECT v FROM ordering WHERE v='24:00'", [][]any{{"24:00:00"}}}, {"SELECT v FROM ordering WHERE v=NULL", nil}} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.postgres, []string{"TIME"}, tc.rows))
				run(s)
				run(referencePrepared(s))
			}
		})
	}
}
