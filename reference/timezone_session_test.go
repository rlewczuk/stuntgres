package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestReferenceTimezonePhysicalReplacementAndDefaultAliases(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("UPDATE tz_witness SET z='2004-01-01 12:00+00' WHERE id=1", 1))
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defaultZone := "UTC"
		if backend.pg {
			defaultZone = "Etc/UTC"
		}
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
		for _, q := range []string{"SET TIME ZONE LOCAL", "SET TIME ZONE DEFAULT"} {
			run(referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
			run(referenceExec(q, 0))
			run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
		}
		run(referenceExec("SET DateStyle='German'", 0))
		run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"German, DMY"}}))
		run(referenceExec("SET DateStyle='DEFAULT,DMY'", 0))
		run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
		run(referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
		if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		// Raw returning ErrBadConn already discards and closes this sql.Conn.
		if err := c.Close(); !errors.Is(err, sql.ErrConnDone) {
			t.Fatalf("discarded connection Close: got %v, want sql.ErrConnDone", err)
		}
		fresh, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		referenceRunStep(t, p.ctx, fresh, backend.recorder, referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
		referenceRunStep(t, p.ctx, fresh, backend.recorder, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
		referenceRunStep(t, p.ctx, fresh, backend.recorder, referenceSelect("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"2004-01-01 12:00:00+00"}}))
	}
}

func TestReferenceTimezoneSettingsTransactionLifecycle(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, backend := range p.backends() {
		backend.db.SetMaxOpenConns(2)
		backend.db.SetMaxIdleConns(2)
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peer, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, prepared := range []bool{false, true} {
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			run(referenceExec("SET DateStyle='ISO,MDY'", 0))
			run(referenceExec("SET DateStyle='DMY'", 0))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
			referenceRunStep(t, p.ctx, peer, backend.recorder, referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
			run(referenceExec("RESET TIME ZONE", 0))
			defaultZone := "UTC"
			if backend.pg {
				defaultZone = "Etc/UTC"
			}
			run(referenceSelect("SELECT current_setting('TimeZone')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{defaultZone}}))
			run(referenceSelect("SELECT set_config('TimeZone','America/New_York',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			for _, q := range []string{"BEGIN", "SET LOCAL TIME ZONE 'Asia/Tokyo'", "SET LOCAL DateStyle='SQL,MDY'", "SAVEPOINT mark", "SET TIME ZONE 'UTC'", "SET DateStyle='German'", "ROLLBACK TO mark"} {
				run(referenceExec(q, 0))
			}
			run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, MDY"}}))
			run(referenceExec("COMMIT", 0))
			run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
			for _, q := range []string{"BEGIN", "SET TIME ZONE 'Asia/Tokyo'", "SET DateStyle='German'", "ROLLBACK"} {
				run(referenceExec(q, 0))
			}
			run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			referenceRunStep(t, p.ctx, peer, backend.recorder, referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
			run(referenceExec("RESET ALL", 0))
			run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
		}
		referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		again, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer again.Close()
		referenceRunStep(t, p.ctx, again, backend.recorder, referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	}
}
