package reference_test

import (
	"database/sql/driver"
	"testing"
)

func TestReferenceTimezoneAbbreviationsIsolationReplacement(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			backend.db.SetMaxOpenConns(2)
			backend.db.SetMaxIdleConns(2)
			a, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
			for _, x := range []referenceExecutor{a, b} {
				run(x, referenceExec("SET TIME ZONE 'UTC'", 0))
				run(x, referenceExec("SET DateStyle='ISO,MDY'", 0))
			}
			run(a, referenceExec("SET timezone_abbreviations='India'", 0))
			run(b, timezoneAbbreviationsShow("Default"))
			query := "SELECT ($1::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS v FROM tz_witness"
			for range 2 {
				run(a, referencePrepared(referenceSelect(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 06:30:00"}}, "2004-07-01 12:00 IST")))
				run(b, referencePrepared(referenceSelect(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}, "2004-07-01 12:00 IST")))
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			run(a, timezoneAbbreviationsShow("India"))
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
				t.Fatal(err)
			}
			a, err = backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			run(a, timezoneAbbreviationsShow("Default"))
			run(a, referenceSelect(query, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 10:00:00"}}, "2004-07-01 12:00 IST"))
		})
	}
}
