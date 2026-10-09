package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func localReferenceSelect(postgres bool, q, column, want string) referenceStep {
	s := referenceSelect(q, []string{column}, []string{"TEXT"}, [][]any{{want}})
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		settingsTextMetadata(t, rows, postgres, strings.HasPrefix(q, "SELECT"))
	}
	return s
}
func TestReferenceSettingsLocalTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, commit := range []bool{false, true} {
			for _, tc := range []struct {
				name         string
				commands     []string
				visible, end string
			}{
				{"Session", []string{"SET SESSION application_name='s'"}, "s", "s"},
				{"Local", []string{"SET LOCAL application_name='l'"}, "l", "b"},
				{"SessionLocal", []string{"SET application_name='s'", "SET LOCAL application_name='l'"}, "l", "s"},
				{"LocalSession", []string{"SET LOCAL application_name='l'", "SET application_name='s'"}, "s", "s"},
				{"Repeated", []string{"SET application_name='s'", "SET LOCAL application_name='l'", "SET application_name='t'", "SET LOCAL application_name='u'"}, "u", "t"},
				{"DefaultAll", []string{"SET application_name='s'", "SET LOCAL application_name=DEFAULT", "RESET ALL"}, "DEFAULT", "s"},
				{"ExplicitDefaultAll", []string{"SET application_name='s'", "SET LOCAL application_name=''", "RESET ALL"}, "DEFAULT", "DEFAULT"},
				{"LocalReset", []string{"SET LOCAL application_name='l'", "RESET application_name"}, "DEFAULT", "DEFAULT"},
				{"ResetLocal", []string{"SET application_name='s'", "RESET application_name", "SET LOCAL application_name='l'"}, "l", "DEFAULT"},
				{"Function", []string{"SELECT set_config('application_name','s',false)", "SELECT set_config('application_name','l',true)"}, "l", "s"},
				{"FunctionNull", []string{"SET LOCAL application_name='l'", "SELECT set_config('application_name',NULL,NULL)"}, "DEFAULT", "DEFAULT"},
			} {
				t.Run(fmt.Sprintf("%s/prepared=%v/commit=%v", tc.name, prepared, commit), func(t *testing.T) {
					for _, backend := range p.backends() {
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
						var d, peer string
						if err = a.QueryRowContext(p.ctx, "SHOW application_name").Scan(&d); err != nil {
							t.Fatal(err)
						}
						if err = b.QueryRowContext(p.ctx, "SHOW application_name").Scan(&peer); err != nil {
							t.Fatal(err)
						}
						run := func(e referenceExecutor, s referenceStep) {
							s.prepared = prepared
							referenceRunStep(t, p.ctx, e, backend.recorder, s)
						}
						if prepared {
							referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("SET application_name='b'", 0))
						}
						run(a, referenceExec("SET application_name='b'", 0))
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for _, q := range tc.commands {
							count := int64(0)
							if strings.HasPrefix(q, "SELECT") {
								count = 1
							}
							run(tx, referenceExec(q, count))
						}
						visible := tc.visible
						if visible == "DEFAULT" {
							visible = d
						}
						run(tx, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", visible))
						run(tx, localReferenceSelect(backend.pg, "SELECT current_setting('application_name')", "current_setting", visible))
						referenceRunStep(t, p.ctx, b, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", peer))
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						end := tc.end
						if end == "DEFAULT" {
							end = d
						}
						if !commit {
							end = "b"
						}
						run(a, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", end))
						// Restore captured baseline before reusing a pooled physical session.
						referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("RESET application_name", 0))
					}
				})
			}
		}
	}
}

func TestReferenceSettingsLocalImplicit(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		var notices []referenceDiagnostic
		_, c, record := noticeReferencePool(t, p, postgres, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
		run(referenceExec("SET application_name='b'", 0))
		for _, prepared := range []bool{false, true} {
			before := len(notices)
			s := referenceExec("SET LOCAL application_name='l'", 0)
			s.prepared = prepared
			run(s)
			want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
			if len(notices) != before+1 || notices[before] != want {
				t.Fatal(notices)
			}
			run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
			if prepared {
				run(referencePrepared(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b")))
			}
			before = len(notices)
			s = localReferenceSelect(backend.pg, "SELECT set_config('application_name','transient',true)", "set_config", "transient")
			s.prepared = prepared
			run(s)
			if len(notices) != before {
				t.Fatal("function warned", notices)
			}
			run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
			if prepared {
				run(referencePrepared(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b")))
			}
		}
		before := len(notices)
		statement, err := c.PrepareContext(p.ctx, "SET LOCAL new.implicit='x'")
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		// Knowledge is created at Execute, never Prepare.
		unknown := settingsReferenceError("SHOW new.implicit", "42704", `unrecognized configuration parameter "new.implicit"`, "", "")
		run(unknown)
		if len(notices) != before {
			t.Fatal("Prepare warned")
		}
		if _, err = statement.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		}
		statement.Close()
		warning := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
		if len(notices) != before+1 || notices[before] != warning {
			t.Fatal("held LOCAL warning", notices[before:])
		}
		run(localReferenceSelect(backend.pg, "SHOW new.implicit", "new.implicit", ""))
		run(localReferenceSelect(backend.pg, "SELECT set_config('new.function','transient',true)", "set_config", "transient"))
		before = len(notices)
		run(referencePrepared(localReferenceSelect(backend.pg, "SELECT set_config('new.function','transient',true)", "set_config", "transient")))
		if len(notices) != before {
			t.Fatal("function SQL warning", notices[before:])
		}
		run(localReferenceSelect(backend.pg, "SHOW new.function", "new.function", ""))
		original := strings.Repeat("a", 64)
		clipped := strings.Repeat("a", 63)
		run(referenceExec("SET client_min_messages=error", 0))
		before = len(notices)
		run(referenceExec("SET LOCAL client_min_messages=notice", 0))
		if len(notices) != before {
			t.Fatal("warning ignored prior threshold", notices[before:])
		}
		run(localReferenceSelect(backend.pg, "SHOW client_min_messages", "client_min_messages", "error"))
		run(referenceExec("RESET client_min_messages", 0))
		before = len(notices)
		run(referencePrepared(referenceExec("SET LOCAL application_name='"+original+"'", 0)))
		truncation := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
		if len(notices) != before+2 || notices[before] != warning || notices[before+1] != truncation {
			t.Fatal("transient normalization order", notices)
		}
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
		before = len(notices)
		run(referencePrepared(referenceSelect("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, original)))
		if len(notices) != before+1 || notices[before] != truncation {
			t.Fatal("function must emit only its normalizer NOTICE", notices)
		}
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "b"))
		run(referenceExec("RESET ALL", 0))
		run(localReferenceSelect(backend.pg, "SHOW new.implicit", "new.implicit", ""))
	}
}

func TestReferenceSettingsLocalFunctionsAndDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	// Native typed registry is a public host API, not a PostgreSQL custom-GUC type.
	a, b := settingsLocalConn(t)
	for _, q := range []string{"SET demo.flag=on", "SET demo.text='session'", "SET demo.count=3", "SET demo.ratio='2.5'", "SET demo.mode=fast"} {
		settingsLocalExec(t, a, q, false)
	}
	typed, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Rollback()
	for _, q := range []string{"SET LOCAL demo.flag=off", "SET LOCAL demo.text=''", "SET LOCAL demo.count=4", "SET LOCAL demo.ratio='3.5'", "SET LOCAL demo.mode=safe", "SET new.placeholder='recognized'"} {
		settingsLocalExec(t, typed, q, true)
	}
	if err = typed.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{{"demo.flag", "on"}, {"demo.text", "session"}, {"demo.count", "3"}, {"demo.ratio", "2.5"}, {"demo.mode", "fast"}, {"new.placeholder", ""}} {
		settingsRows(t, a, "SHOW "+tc.name, false, tc.name, tc.want)
	}
	_, err = b.ExecContext(p.ctx, "SHOW new.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "new.placeholder"`, "", "", "")
	settingsLocalExec(t, a, "RESET ALL", true)
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var d string
		if err = c.QueryRowContext(p.ctx, "SHOW application_name").Scan(&d); err != nil {
			t.Fatal(err)
		}
		for _, commit := range []bool{false, true} {
			for _, equalDefault := range []bool{false, true} {
				run := func(e referenceExecutor, s referenceStep) {
					s.prepared = true
					referenceRunStep(t, p.ctx, e, backend.recorder, s)
				}
				run(c, referenceExec("SET application_name='b'", 0))
				tx, err := c.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(tx, localReferenceSelect(backend.pg, "SELECT set_config('application_name','s',false)", "set_config", "s"))
				if equalDefault {
					run(tx, referenceSelect("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{d}}, d))
				} else {
					run(tx, referenceSelect("SELECT set_config('application_name',NULL,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{d}}))
				}
				run(tx, referenceExec("RESET ALL", 0))
				run(tx, localReferenceSelect(backend.pg, "SELECT current_setting('application_name')", "current_setting", d))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				want := "b"
				if commit {
					want = "s"
					if equalDefault {
						want = d
					}
				}
				run(c, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want))
			}
		}
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("SET new.rollback='x'", 0))
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW new.rollback", "new.rollback", ""))
		referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("RESET ALL", 0))
		referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW new.rollback", "new.rollback", ""))
	}
}
