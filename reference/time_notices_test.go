package reference_test

import (
	"reflect"
	"testing"
	"time"
)

func TestReferenceTimeNoticePhases(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			record.rawBackend = referenceRawNative
			if postgres {
				record.rawBackend = referenceRawPostgres
			}
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, conn, record, s) }
			run(referenceExec("CREATE TABLE anchor(v TIME)", 0))
			run(referenceExec("INSERT INTO anchor VALUES('00:00')", 1))
			stmt, err := conn.PrepareContext(p.ctx, "SELECT TIME(7) '04:05:06.1234567' FROM anchor")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(7) precision reduced to maximum allowed, 6", Position: "8"}
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			x := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: "SELECT TIME(7) '04:05:06.1234567' FROM anchor"}
			for range 2 {
				referenceTemporalRunStep(t, p.ctx, x, record, referenceSelect(x.query, []string{"time"}, []string{"TIME"}, referenceTimeRows(t, postgres, []string{"TIME"}, [][]any{{"04:05:06.123457"}})))
			}
			if len(notices) != 0 {
				t.Fatal("warning repeated", notices)
			}
			utility, err := conn.PrepareContext(p.ctx, "CREATE TABLE capped(v TIME(7))")
			if err != nil || len(notices) != 0 {
				t.Fatal(err, notices)
			}
			_, err = utility.ExecContext(p.ctx)
			utility.Close()
			if err != nil {
				t.Fatal(err)
			}
			want.Position = "23"
			unpositioned := want
			unpositioned.Position = ""
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want, unpositioned}) {
				t.Fatal(notices)
			}
			notices = nil
			run(referenceExec("INSERT INTO capped VALUES('00:00')", 1))
			run(referenceExec("CREATE TABLE zoned(v TIME WITH TIME ZONE)", 0))
			run(referenceExec("INSERT INTO zoned VALUES('04:05+02')", 1))
			var zoned any = "04:05:00+02"
			if postgres {
				zoned = time.Date(0, 1, 1, 2, 5, 0, 0, time.UTC)
			}
			run(referenceSelect("SELECT v FROM zoned", []string{"v"}, []string{"TIMETZ"}, [][]any{{zoned}}))
			literalQuery := "SELECT TIME(7) 'now' FROM capped"
			literal, err := conn.PrepareContext(p.ctx, literalQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := literal.Close(); err != nil {
					t.Error(err)
				}
			}()
			want.Position = "8"
			if !reflect.DeepEqual(notices, []referenceDiagnostic{want}) {
				t.Fatal(notices)
			}
			notices = nil
			var first any
			for i := range 2 {
				var clock any
				if err := literal.QueryRowContext(p.ctx).Scan(&clock); err != nil {
					t.Fatal(err)
				}
				referenceAssertRawValue(t, postgres, "TIME", clock)
				if i == 0 {
					first = referenceNormalize(clock)
				} else if !reflect.DeepEqual(referenceNormalize(clock), first) {
					t.Fatal("prepared now changed", clock, first)
				}
			}
			if len(notices) != 0 {
				t.Fatal("warning repeated", notices)
			}
		})
	}
}
