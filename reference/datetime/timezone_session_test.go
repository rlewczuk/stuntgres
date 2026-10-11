package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezonePhysicalReplacementAndDefaultAliases(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='2004-01-01 12:00+00' WHERE id=1", 1))
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defaultZone := "UTC"
		if backend.PG {
			defaultZone = "Etc/UTC"
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
		for _, q := range []string{"SET TIME ZONE LOCAL", "SET TIME ZONE DEFAULT"} {
			run(fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
			run(fixture.Exec(q, 0))
			run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
		}
		run(fixture.Exec("SET DateStyle='German'", 0))
		run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"German, DMY"}}))
		run(fixture.Exec("SET DateStyle='DEFAULT,DMY'", 0))
		run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
		run(fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
		if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		// Raw returning ErrBadConn already discards and closes this sql.Conn.
		if err := c.Close(); !errors.Is(err, sql.ErrConnDone) {
			t.Fatalf("discarded connection Close: got %v, want sql.ErrConnDone", err)
		}
		fresh, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
		fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
		fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, fixture.Select("SELECT z::TEXT AS z FROM tz_witness", []string{"z"}, []string{"TEXT"}, [][]any{{"2004-01-01 12:00:00+00"}}))
	}
}

func TestReferenceTimezoneSettingsTransactionLifecycle(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		backend.DB.SetMaxOpenConns(2)
		backend.DB.SetMaxIdleConns(2)
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peer, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, prepared := range []bool{false, true} {
			run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
			run(fixture.Exec("SET DateStyle='DMY'", 0))
			run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
			fixture.RunStep(t, p.Ctx, peer, backend.Recorder, fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
			run(fixture.Exec("RESET TIME ZONE", 0))
			defaultZone := "UTC"
			if backend.PG {
				defaultZone = "Etc/UTC"
			}
			run(fixture.Select("SELECT current_setting('TimeZone')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{defaultZone}}))
			run(fixture.Select("SELECT set_config('TimeZone','America/New_York',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			for _, q := range []string{"BEGIN", "SET LOCAL TIME ZONE 'Asia/Tokyo'", "SET LOCAL DateStyle='SQL,MDY'", "SAVEPOINT mark", "SET TIME ZONE 'UTC'", "SET DateStyle='German'", "ROLLBACK TO mark"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
			run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, MDY"}}))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, DMY"}}))
			for _, q := range []string{"BEGIN", "SET TIME ZONE 'Asia/Tokyo'", "SET DateStyle='German'", "ROLLBACK"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
			fixture.RunStep(t, p.Ctx, peer, backend.Recorder, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{defaultZone}}))
			run(fixture.Exec("RESET ALL", 0))
			run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"ISO, MDY"}}))
		}
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		again, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer again.Close()
		fixture.RunStep(t, p.Ctx, again, backend.Recorder, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	}
}
