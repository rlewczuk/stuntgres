package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsLocalNoticeLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, baseline := range []string{"notice", "error"} {
					t.Run(fmt.Sprintf("postgres=%v/prepared=%v/commit=%v/baseline=%s", postgres, prepared, commit, baseline), func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
						run := func(e fixture.Executor, s fixture.Step) {
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, e, record, s)
						}
						run(c, fixture.Exec("SET application_name='b'", 0))
						run(c, fixture.Exec("SET client_min_messages="+baseline, 0))
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						local := "error"
						if baseline == "error" {
							local = "notice"
						}
						run(tx, fixture.Exec("SET LOCAL client_min_messages="+local, 0))
						original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
						step := fixture.Select("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, original)
						before := len(notices)
						run(tx, step)
						count := 0
						if local == "notice" {
							count = 1
						}
						if len(notices) != before+count || (count == 1 && notices[before] != want) {
							t.Fatal("LOCAL notice filtering", notices)
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						run(c, localReferenceSelect(backend.PG, "SHOW client_min_messages", "client_min_messages", baseline))
						run(c, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "b"))
						before = len(notices)
						run(c, step)
						count = 1 - count
						if len(notices) != before+count || (count == 1 && notices[before] != want) {
							t.Fatal("restored notice filtering", notices)
						}
						run(c, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "b"))
					})
				}
			}
		}
	}
}

func TestReferenceSettingsLocalResetAllBatch(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("postgres=%v/prepared=%v", postgres, prepared), func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
				run := func(e fixture.Executor, s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, e, record, s)
				}
				// application_name may have a backend startup override: capture its
				// reset source independently, never infer it from the native value.
				var applicationDefault string
				if err := c.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&applicationDefault); err != nil {
					t.Fatal(err)
				}
				settings := []struct{ name, original, changed, reset string }{
					{"application_name", "original", "changed", applicationDefault},
					{"standard_conforming_strings", "off", "on", "on"},
					{"statement_timeout", "2s", "3s", "0"},
					{"client_min_messages", "warning", "error", "notice"},
					{"batch.placeholder", "original", "changed", ""},
				}
				for _, s := range settings[:4] {
					run(c, localReferenceSelect(backend.PG, "SHOW "+s.name, s.name, s.reset))
				}
				for _, s := range settings {
					run(c, fixture.Exec("SET "+s.name+"='"+s.original+"'", 0))
				}
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range settings {
					run(tx, fixture.Exec("SET LOCAL "+s.name+"='"+s.changed+"'", 0))
					run(tx, localReferenceSelect(backend.PG, "SHOW "+s.name, s.name, s.changed))
				}
				run(tx, fixture.Exec("RESET ALL", 0))
				for _, s := range settings {
					run(tx, localReferenceSelect(backend.PG, "SHOW "+s.name, s.name, s.reset))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				for _, s := range settings {
					run(c, localReferenceSelect(backend.PG, "SHOW "+s.name, s.name, s.original))
				}
				if len(notices) != 0 {
					t.Fatal("explicit batch emitted notices", notices)
				}
			})
		}
	}
}

func TestReferenceSettingsLocalSQLControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		for _, prepared := range []bool{false, true} {
			for _, end := range []string{"COMMIT", "END WORK", "ROLLBACK", "ABORT TRANSACTION"} {
				for _, chain := range []bool{false, true} {
					for _, aborted := range []bool{false, true} {
						t.Run(fmt.Sprintf("postgres=%v/prepared=%v/end=%s/chain=%v/aborted=%v", postgres, prepared, end, chain, aborted), func(t *testing.T) {
							var notices []fixture.Diagnostic
							_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
							run := func(s fixture.Step) {
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, record, s)
							}
							run(fixture.Exec("SET application_name='b'", 0))
							run(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							run(fixture.Exec("SET application_name='s'", 0))
							run(fixture.Exec("SET LOCAL application_name='l'", 0))
							run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "l"))
							if aborted {
								run(settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
								unreachable := settingsReferenceError("SET application_name='unreachable'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "")
								if prepared {
									unreachable.ErrorStage = "prepare"
								}
								run(unreachable)
							}
							q := end
							if chain {
								q += " AND CHAIN"
							}
							run(fixture.Exec(q, 0))
							want := "b"
							if !aborted && (end == "COMMIT" || end == "END WORK") {
								want = "s"
							}
							run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", want))
							if chain {
								run(fixture.Exec("SET LOCAL application_name='n'", 0))
								run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "n"))
								run(fixture.Exec("ROLLBACK", 0))
								run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", want))
							}
							if prepared && aborted {
								// The original aborted commands were direct even in the
								// held-control matrix. Keep a fresh transaction so their
								// 42704/25P02 stages cannot corrupt the recorded phase.
								run(fixture.Exec("BEGIN", 0))
								run(fixture.Exec("SET application_name='s'", 0))
								run(fixture.Exec("SET LOCAL application_name='l'", 0))
								run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "l"))
								fixture.RunStep(t, p.Ctx, c, record, settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
								fixture.RunStep(t, p.Ctx, c, record, settingsReferenceError("SET application_name='unreachable'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", ""))
								run(fixture.Exec(q, 0))
								run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", want))
								if chain {
									run(fixture.Exec("SET LOCAL application_name='n'", 0))
									run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "n"))
									run(fixture.Exec("ROLLBACK", 0))
									run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", want))
								}
							}
							if len(notices) != 0 {
								t.Fatal("SQL settings lifecycle emitted notices", notices)
							}
						})
					}
				}
			}
		}
	}
}
