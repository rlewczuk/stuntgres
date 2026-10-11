package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSettingsSetConfigReentry(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Select("SELECT set_config('application_name','healthy',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"healthy"}}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, prepared := range []bool{false, true} {
		d := stuntgres.NewDriver()
		raw, err := d.OpenConnector("set-config-notices")
		if err != nil {
			t.Fatal(err)
		}
		var physical driver.QueryerContext
		var notices []stuntgres.Error
		var callbackError error
		configured := raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
			notices = append(notices, n)
			// Reenter the same admitted adapter, not database/sql's held Conn mutex.
			// This witnesses completion of the lease as well as publication.
			rows, e := physical.QueryContext(ctx, "SHOW application_name", nil)
			if e != nil {
				callbackError = e
				return
			}
			defer rows.Close()
			value := make([]driver.Value, 1)
			if e = rows.Next(value); e != nil {
				callbackError = e
				return
			}
			if value[0] != strings.Repeat("a", 63) {
				callbackError = errors.New("callback observed unpublished value")
			}
		})
		db := sql.OpenDB(configured)
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Raw(func(v any) error { physical = v.(driver.QueryerContext); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(notices) != 0 {
			t.Fatal("default initialization emitted notice", notices)
		}
		original := strings.Repeat("a", 64)
		clipped := strings.Repeat("a", 63)
		want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
		for _, threshold := range []string{"notice", "warning", "error"} {
			sessionSet(t, c, "SET client_min_messages TO "+threshold, false)
			before := len(notices)
			checkNotices := func(commands int) {
				t.Helper()
				count := 0
				if threshold == "notice" {
					count = commands
				}
				if len(notices) != before+count || callbackError != nil {
					t.Fatal(notices, callbackError)
				}
				for _, notice := range notices[before:] {
					if notice != want {
						t.Fatal("notice diagnostic", notice, want)
					}
				}
			}
			sessionSet(t, c, "SET application_name TO '"+original+"'", prepared)
			checkNotices(1)
			q := "SELECT set_config($1,$2,false)"
			if prepared {
				beforePrepare := len(notices)
				s, e := c.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				if len(notices) != beforePrepare {
					t.Fatal("prepare emitted notice")
				}
				settingsRows(t, c, "SHOW application_name", false, "application_name", clipped)
				rows, e := s.QueryContext(ctx, "application_name", original)
				if e != nil {
					t.Fatal(e)
				}
				rows.Close()
				s.Close()
			} else {
				settingsRows(t, c, q, false, "set_config", clipped, "application_name", original)
			}
			checkNotices(2)
			sessionSet(t, c, "SET application_name TO '"+original+"'", prepared)
			checkNotices(3)
			settingsRows(t, c, "SHOW application_name", false, "application_name", clipped)
		}
		sessionSet(t, c, "RESET client_min_messages", false)
		before := len(notices)
		settingsRows(t, c, "SELECT set_config('application_name',NULL,false)", prepared, "set_config", "")
		sessionSet(t, c, "SET application_name TO DEFAULT", prepared)
		sessionSet(t, c, "RESET application_name", prepared)
		if len(notices) != before {
			t.Fatal("default restore emitted notice", notices)
		}
		c.Close()
		db.Close()
	}
}

// The callback adapter above is native-only. This independent shared schedule
// establishes the notice count and all diagnostic fields against PostgreSQL.
func TestReferenceSettingsClippingNoticeSchedule(t *testing.T) {
	p := fixture.NewScenario(t)
	original := strings.Repeat("a", 64)
	clipped := strings.Repeat("a", 63)
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
	for _, backend := range p.Backends() {
		var notices []fixture.Diagnostic
		_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		for _, prepared := range []bool{false, true} {
			run := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, record, s)
			}
			for _, threshold := range []string{"notice", "warning", "error"} {
				run(fixture.Exec("SET client_min_messages TO "+threshold, 0))
				checkCommand := func(before int) {
					t.Helper()
					count := 0
					if threshold == "notice" {
						count = 1
					}
					if len(notices) != before+count {
						t.Fatal("per-command notice count", threshold, notices[before:])
					}
					for _, notice := range notices[before:] {
						if notice != want {
							t.Fatal("notice diagnostic", notice, want)
						}
					}
				}
				before := len(notices)
				run(fixture.Exec("SET application_name TO '"+original+"'", 0))
				checkCommand(before)
				before = len(notices)
				q := "SELECT set_config($1,$2,false)"
				step := fixture.Select(q, []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, "application_name", original)
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if len(notices) != before {
						t.Fatal("Prepare emitted notice", notices[before:])
					}
					fixture.RunStep(t, p.Ctx, c, record, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{clipped}}))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, record, step)
					stmt.Close()
				} else {
					run(step)
				}
				checkCommand(before)
				before = len(notices)
				run(fixture.Exec("SET application_name TO '"+original+"'", 0))
				checkCommand(before)
			}
			run(fixture.Exec("RESET client_min_messages", 0))
			before := len(notices)
			run(fixture.Select("SELECT set_config('application_name',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}))
			run(fixture.Exec("SET application_name TO DEFAULT", 0))
			run(fixture.Exec("RESET application_name", 0))
			if len(notices) != before {
				t.Fatal("default restore emitted notice", notices[before:])
			}
		}
	}
}
