package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeNoticePhases(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			record.RawBackend = fixture.RawNative
			if postgres {
				record.RawBackend = fixture.RawPostgres
			}
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, conn, record, s) }
			run(fixture.Exec("CREATE TABLE anchor(v TIME)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES('00:00')", 1))
			stmt, err := conn.PrepareContext(p.Ctx, "SELECT TIME(7) '04:05:06.1234567' FROM anchor")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(7) precision reduced to maximum allowed, 6", Position: "8"}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatal(notices, want)
			}
			notices = nil
			x := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: "SELECT TIME(7) '04:05:06.1234567' FROM anchor"}
			for range 2 {
				fixture.TemporalRunStep(t, p.Ctx, x, record, fixture.Select(x.SQL, []string{"time"}, []string{"TIME"}, fixture.TimeRows(t, postgres, []string{"TIME"}, [][]any{{"04:05:06.123457"}})))
			}
			if len(notices) != 0 {
				t.Fatal("warning repeated", notices)
			}
			utility, err := conn.PrepareContext(p.Ctx, "CREATE TABLE capped(v TIME(7))")
			if err != nil || len(notices) != 0 {
				t.Fatal(err, notices)
			}
			_, err = utility.ExecContext(p.Ctx)
			utility.Close()
			if err != nil {
				t.Fatal(err)
			}
			want.Position = "23"
			unpositioned := want
			unpositioned.Position = ""
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, unpositioned}) {
				t.Fatal(notices)
			}
			notices = nil
			run(fixture.Exec("INSERT INTO capped VALUES('00:00')", 1))
			run(fixture.Exec("CREATE TABLE zoned(v TIME WITH TIME ZONE)", 0))
			run(fixture.Exec("INSERT INTO zoned VALUES('04:05+02')", 1))
			var zoned any = "04:05:00+02"
			if postgres {
				zoned = time.Date(0, 1, 1, 2, 5, 0, 0, time.UTC)
			}
			run(fixture.Select("SELECT v FROM zoned", []string{"v"}, []string{"TIMETZ"}, [][]any{{zoned}}))
			literalQuery := "SELECT TIME(7) 'now' FROM capped"
			literal, err := conn.PrepareContext(p.Ctx, literalQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := literal.Close(); err != nil {
					t.Error(err)
				}
			}()
			want.Position = "8"
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatal(notices)
			}
			notices = nil
			var first any
			for i := range 2 {
				var clock any
				if err := literal.QueryRowContext(p.Ctx).Scan(&clock); err != nil {
					t.Fatal(err)
				}
				fixture.AssertRawValue(t, postgres, "TIME", clock)
				if i == 0 {
					first = fixture.Normalize(clock)
				} else if !reflect.DeepEqual(fixture.Normalize(clock), first) {
					t.Fatal("prepared now changed", clock, first)
				}
			}
			if len(notices) != 0 {
				t.Fatal("warning repeated", notices)
			}
		})
	}
}
