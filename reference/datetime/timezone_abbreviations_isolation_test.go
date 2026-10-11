package reference_test

import (
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneAbbreviationsIsolationReplacement(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			backend.DB.SetMaxOpenConns(2)
			backend.DB.SetMaxIdleConns(2)
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
			for _, x := range []fixture.Executor{a, b} {
				run(x, fixture.Exec("SET TIME ZONE 'UTC'", 0))
				run(x, fixture.Exec("SET DateStyle='ISO,MDY'", 0))
			}
			run(a, fixture.Exec("SET timezone_abbreviations='India'", 0))
			run(b, timezoneAbbreviationsShow("Default"))
			query := "SELECT ($1::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
			for range 2 {
				run(a, fixture.Prepared(fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 06:30:00"}}, "2004-07-01 12:00 IST")))
				run(b, fixture.Prepared(fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}, "2004-07-01 12:00 IST")))
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			run(a, timezoneAbbreviationsShow("India"))
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
				t.Fatal(err)
			}
			a, err = backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			run(a, timezoneAbbreviationsShow("Default"))
			run(a, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}, "2004-07-01 12:00 IST"))
		})
	}
}
