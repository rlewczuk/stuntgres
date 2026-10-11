//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimeOptionalDateCompletion(t *testing.T) {
	p := fixture.NewPair(t)
	for _, backend := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *fixture.Recorder
	}{{"native", false, p.Memory, p.MemoryRecord}, {"postgres", true, p.Postgres, p.PostgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.db, backend.record, s) }
			run(fixture.Exec("CREATE TABLE t(v TIME)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('00:00')", 1))
			for _, text := range []string{"2003.032 04:05 Etc/UTC", "2000.366 04:05 Etc/UTC", "1999.366 04:05 Etc/UTC", "07-Mar-2003 04:05", "2003-07-07 04:05:06+02"} {
				want := "04:05:00"
				if text == "2003-07-07 04:05:06+02" {
					want = "04:05:06"
				}
				s := fixture.Select("SELECT TIME '"+text+"' FROM t", []string{"time"}, []string{"TIME"}, fixture.TimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{want}}))
				run(s)
				run(fixture.Prepared(s))
			}
			for _, text := range []string{"2003.032 04:05", "2003-07-07T04:05", "04:05 2003-07-07 America/New_York", "2003-07-07 0405", "040506 2003-07-07", "j2451545.5 04:05", "j2451545.5 j2451545.5"} {
				want := fixture.Diagnostic{Code: "22007", Message: "invalid input syntax for type time: \"" + text + "\"", Position: "13"}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT TIME '"+text+"' FROM t", want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		})
	}
}
