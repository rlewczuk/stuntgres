package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSettingsSetConfigReentry(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceSelect("SELECT set_config('application_name','healthy',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"healthy"}}))
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
		original := strings.Repeat("a", 64)
		clipped := strings.Repeat("a", 63)
		want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
		for _, threshold := range []string{"notice", "warning", "error"} {
			sessionSet(t, c, "SET client_min_messages TO "+threshold, false)
			before := len(notices)
			q := "SELECT set_config($1,$2,false)"
			if prepared {
				s, e := c.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				if len(notices) != before {
					t.Fatal("prepare emitted notice")
				}
				settingsRows(t, c, "SHOW application_name", false, "application_name", map[bool]string{true: clipped, false: ""}[before > 0])
				rows, e := s.QueryContext(ctx, "application_name", original)
				if e != nil {
					t.Fatal(e)
				}
				rows.Close()
				s.Close()
			} else {
				settingsRows(t, c, q, false, "set_config", clipped, "application_name", original)
			}
			count := 0
			if threshold == "notice" {
				count = 1
			}
			if len(notices) != before+count || callbackError != nil {
				t.Fatal(notices, callbackError)
			}
			if count == 1 && notices[len(notices)-1] != want {
				t.Fatal(notices)
			}
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
