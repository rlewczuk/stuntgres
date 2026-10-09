package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceSettingsLocalNoticeLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, baseline := range []string{"notice", "error"} {
					t.Run(fmt.Sprintf("postgres=%v/prepared=%v/commit=%v/baseline=%s", postgres, prepared, commit, baseline), func(t *testing.T) {
						var notices []referenceDiagnostic
						_, c, record := noticeReferencePool(t, p, postgres, &notices)
						run := func(e referenceExecutor, s referenceStep) {
							s.prepared = prepared
							referenceRunStep(t, p.ctx, e, record, s)
						}
						run(c, referenceExec("SET application_name='b'", 0))
						run(c, referenceExec("SET client_min_messages="+baseline, 0))
						tx, err := c.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						local := "error"
						if baseline == "error" {
							local = "notice"
						}
						run(tx, referenceExec("SET LOCAL client_min_messages="+local, 0))
						original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
						want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
						step := referenceSelect("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, original)
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
						run(c, localReferenceSelect(backend.pg, "SHOW client_min_messages", "client_min_messages", baseline))
						run(c, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
						before = len(notices)
						run(c, step)
						count = 1 - count
						if len(notices) != before+count || (count == 1 && notices[before] != want) {
							t.Fatal("restored notice filtering", notices)
						}
						run(c, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
					})
				}
			}
		}
	}
}

func TestReferenceSettingsLocalResetAllBatch(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("postgres=%v/prepared=%v", postgres, prepared), func(t *testing.T) {
				var notices []referenceDiagnostic
				_, c, record := noticeReferencePool(t, p, postgres, &notices)
				run := func(e referenceExecutor, s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, e, record, s)
				}
				// application_name may have a backend startup override: capture its
				// reset source independently, never infer it from the native value.
				var applicationDefault string
				if err := c.QueryRowContext(p.ctx, "SHOW application_name").Scan(&applicationDefault); err != nil {
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
					run(c, localReferenceSelect(backend.pg, "SHOW "+s.name, s.name, s.reset))
				}
				for _, s := range settings {
					run(c, referenceExec("SET "+s.name+"='"+s.original+"'", 0))
				}
				tx, err := c.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range settings {
					run(tx, referenceExec("SET LOCAL "+s.name+"='"+s.changed+"'", 0))
					run(tx, localReferenceSelect(backend.pg, "SHOW "+s.name, s.name, s.changed))
				}
				run(tx, referenceExec("RESET ALL", 0))
				for _, s := range settings {
					run(tx, localReferenceSelect(backend.pg, "SHOW "+s.name, s.name, s.reset))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				for _, s := range settings {
					run(c, localReferenceSelect(backend.pg, "SHOW "+s.name, s.name, s.original))
				}
				if len(notices) != 0 {
					t.Fatal("explicit batch emitted notices", notices)
				}
			})
		}
	}
}

func TestReferenceSettingsLocalSQLControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		for _, prepared := range []bool{false, true} {
			for _, end := range []string{"COMMIT", "END WORK", "ROLLBACK", "ABORT TRANSACTION"} {
				for _, chain := range []bool{false, true} {
					for _, aborted := range []bool{false, true} {
						t.Run(fmt.Sprintf("postgres=%v/prepared=%v/end=%s/chain=%v/aborted=%v", postgres, prepared, end, chain, aborted), func(t *testing.T) {
							var notices []referenceDiagnostic
							_, c, record := noticeReferencePool(t, p, postgres, &notices)
							run := func(s referenceStep) {
								s.prepared = prepared
								referenceRunStep(t, p.ctx, c, record, s)
							}
							run(referenceExec("SET application_name='b'", 0))
							run(referenceExec("BEGIN", 0))
							defer c.ExecContext(p.ctx, "ROLLBACK")
							run(referenceExec("SET application_name='s'", 0))
							run(referenceExec("SET LOCAL application_name='l'", 0))
							run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "l"))
							if aborted {
								run(settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
								unreachable := settingsReferenceError("SET application_name='unreachable'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "")
								if prepared {
									unreachable.errorStage = "prepare"
								}
								run(unreachable)
							}
							q := end
							if chain {
								q += " AND CHAIN"
							}
							run(referenceExec(q, 0))
							want := "b"
							if !aborted && (end == "COMMIT" || end == "END WORK") {
								want = "s"
							}
							run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want))
							if chain {
								run(referenceExec("SET LOCAL application_name='n'", 0))
								run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "n"))
								run(referenceExec("ROLLBACK", 0))
								run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want))
							}
							if prepared && aborted {
								// The original aborted commands were direct even in the
								// held-control matrix. Keep a fresh transaction so their
								// 42704/25P02 stages cannot corrupt the recorded phase.
								run(referenceExec("BEGIN", 0))
								run(referenceExec("SET application_name='s'", 0))
								run(referenceExec("SET LOCAL application_name='l'", 0))
								run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "l"))
								referenceRunStep(t, p.ctx, c, record, settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
								referenceRunStep(t, p.ctx, c, record, settingsReferenceError("SET application_name='unreachable'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", ""))
								run(referenceExec(q, 0))
								run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want))
								if chain {
									run(referenceExec("SET LOCAL application_name='n'", 0))
									run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "n"))
									run(referenceExec("ROLLBACK", 0))
									run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want))
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
