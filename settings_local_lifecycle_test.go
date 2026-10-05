package stuntgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSettingsLocalNoticeLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, baseline := range []string{"notice", "error"} {
				t.Run(fmt.Sprintf("prepared=%v/commit=%v/baseline=%s", prepared, commit, baseline), func(t *testing.T) {
					c, notices := settingsLocalNotices(t)
					settingsLocalExec(t, c, "SET application_name='b'", prepared)
					settingsLocalExec(t, c, "SET client_min_messages="+baseline, prepared)
					tx, err := c.BeginTx(context.Background(), nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					local := "error"
					if baseline == "error" {
						local = "notice"
					}
					settingsLocalExec(t, tx, "SET LOCAL client_min_messages="+local, prepared)
					original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
					want := Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
					before := len(*notices)
					settingsLocalRead(t, tx, "SELECT set_config('application_name',$1,true)", clipped, prepared, original)
					count := 0
					if local == "notice" {
						count = 1
					}
					if len(*notices) != before+count || (count == 1 && (*notices)[before] != want) {
						t.Fatal("LOCAL notice filtering", *notices)
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					settingsRows(t, c, "SHOW client_min_messages", prepared, "client_min_messages", baseline)
					settingsRows(t, c, "SHOW application_name", prepared, "application_name", "b")
					before = len(*notices)
					settingsRows(t, c, "SELECT set_config('application_name',$1,true)", prepared, "set_config", clipped, original)
					count = 1 - count
					if len(*notices) != before+count || (count == 1 && (*notices)[before] != want) {
						t.Fatal("restored notice filtering", *notices)
					}
					settingsRows(t, c, "SHOW application_name", prepared, "application_name", "b")
				})
			}
		}
	}
}

func TestSettingsLocalResetAllBatch(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			c, notices := settingsLocalNotices(t)
			settings := []struct{ name, original, changed, reset string }{
				{"application_name", "original", "changed", ""},
				{"standard_conforming_strings", "off", "on", "on"},
				{"statement_timeout", "2s", "3s", "0"},
				{"client_min_messages", "warning", "error", "notice"},
				{"batch.placeholder", "original", "changed", ""},
			}
			for _, s := range settings[:4] {
				settingsRows(t, c, "SHOW "+s.name, prepared, s.name, s.reset)
			}
			for _, s := range settings {
				settingsLocalExec(t, c, "SET "+s.name+"='"+s.original+"'", prepared)
			}
			tx, err := c.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, s := range settings {
				settingsLocalExec(t, tx, "SET LOCAL "+s.name+"='"+s.changed+"'", prepared)
				settingsLocalRead(t, tx, "SHOW "+s.name, s.changed, prepared)
			}
			settingsLocalExec(t, tx, "RESET ALL", prepared)
			for _, s := range settings {
				settingsLocalRead(t, tx, "SHOW "+s.name, s.reset, prepared)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			for _, s := range settings {
				settingsRows(t, c, "SHOW "+s.name, prepared, s.name, s.original)
			}
			if len(*notices) != 0 {
				t.Fatal("explicit batch emitted notices", *notices)
			}
		})
	}
}

func TestSettingsLocalSQLControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, end := range []string{"COMMIT", "END WORK", "ROLLBACK", "ABORT TRANSACTION"} {
			for _, chain := range []bool{false, true} {
				for _, aborted := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%v/end=%s/chain=%v/aborted=%v", prepared, end, chain, aborted), func(t *testing.T) {
						c, notices := settingsLocalNotices(t)
						settingsLocalExec(t, c, "SET application_name='b'", prepared)
						settingsLocalExec(t, c, "BEGIN", prepared)
						defer c.ExecContext(context.Background(), "ROLLBACK")
						settingsLocalExec(t, c, "SET application_name='s'", prepared)
						settingsLocalExec(t, c, "SET LOCAL application_name='l'", prepared)
						settingsRows(t, c, "SHOW application_name", prepared, "application_name", "l")
						if aborted {
							_, err := c.ExecContext(context.Background(), "SET absent_setting='x'")
							settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
							_, err = c.ExecContext(context.Background(), "SET application_name='unreachable'")
							settingsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "", "")
						}
						q := end
						if chain {
							q += " AND CHAIN"
						}
						settingsLocalExec(t, c, q, prepared)
						want := "b"
						if !aborted && (end == "COMMIT" || end == "END WORK") {
							want = "s"
						}
						settingsRows(t, c, "SHOW application_name", prepared, "application_name", want)
						if chain {
							settingsLocalExec(t, c, "SET LOCAL application_name='n'", prepared)
							settingsRows(t, c, "SHOW application_name", prepared, "application_name", "n")
							settingsLocalExec(t, c, "ROLLBACK", prepared)
							settingsRows(t, c, "SHOW application_name", prepared, "application_name", want)
						}
						if len(*notices) != 0 {
							t.Fatal("SQL settings lifecycle emitted notices", *notices)
						}
					})
				}
			}
		}
	}
}
