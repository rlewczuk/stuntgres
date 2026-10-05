//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func localReferenceSelect(q, column, want string) referenceStep {
	return referenceSelect(q, []string{column}, []string{"TEXT"}, [][]any{{want}})
}
func TestReferenceSettingsLocalTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferencePair(t)
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
			} {
				t.Run(fmt.Sprintf("%s/prepared=%v/commit=%v", tc.name, prepared, commit), func(t *testing.T) {
					var observations [2][]referenceObserved
					for i, backend := range []struct {
						db     *sql.DB
						record *referenceRecorder
					}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
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
							observations[i] = append(observations[i], referenceRunStep(t, p.ctx, e, backend.record, s))
						}
						run(a, referenceExec("SET application_name='b'", 0))
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for _, q := range tc.commands {
							run(tx, referenceExec(q, 0))
						}
						visible := tc.visible
						if visible == "DEFAULT" {
							visible = d
						}
						run(tx, localReferenceSelect("SHOW application_name", "application_name", visible))
						run(tx, localReferenceSelect("SELECT current_setting('application_name')", "current_setting", visible))
						referenceRunStep(t, p.ctx, b, backend.record, localReferenceSelect("SHOW application_name", "application_name", peer))
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
						run(a, localReferenceSelect("SHOW application_name", "application_name", end))
						// Restore captured baseline before reusing a pooled physical session.
						referenceRunStep(t, p.ctx, a, backend.record, referenceExec("RESET application_name", 0))
					}
					// Values dependent on backend startup defaults are independently checked,
					// not equated. All other complete observed records have ordinary parity.
					if tc.visible != "DEFAULT" && tc.end != "DEFAULT" && !reflect.DeepEqual(observations[0], observations[1]) {
						t.Fatal(observations)
					}
				})
			}
		}
	}
}

func TestReferenceSettingsLocalImplicit(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
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
			run(localReferenceSelect("SHOW application_name", "application_name", "b"))
			before = len(notices)
			s = localReferenceSelect("SELECT set_config('application_name','transient',true)", "set_config", "transient")
			s.prepared = prepared
			run(s)
			if len(notices) != before {
				t.Fatal("function warned", notices)
			}
			run(localReferenceSelect("SHOW application_name", "application_name", "b"))
		}
		statement, err := c.PrepareContext(p.ctx, "SET LOCAL new.implicit='x'")
		if err != nil {
			t.Fatal(err)
		}
		before := len(notices)
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
		run(localReferenceSelect("SHOW new.implicit", "new.implicit", ""))
		run(localReferenceSelect("SELECT set_config('new.function','transient',true)", "set_config", "transient"))
		run(localReferenceSelect("SHOW new.function", "new.function", ""))
		original := strings.Repeat("a", 64)
		clipped := strings.Repeat("a", 63)
		before = len(notices)
		run(referencePrepared(referenceExec("SET LOCAL application_name='"+original+"'", 0)))
		warning := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
		truncation := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
		if len(notices) != before+2 || notices[before] != warning || notices[before+1] != truncation {
			t.Fatal("transient normalization order", notices)
		}
		run(localReferenceSelect("SHOW application_name", "application_name", "b"))
		before = len(notices)
		run(referencePrepared(referenceSelect("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, original)))
		if len(notices) != before+1 || notices[before] != truncation {
			t.Fatal("function must emit only its normalizer NOTICE", notices)
		}
		run(localReferenceSelect("SHOW application_name", "application_name", "b"))
		run(referenceExec("RESET ALL", 0))
		run(localReferenceSelect("SHOW new.implicit", "new.implicit", ""))
	}
}

func TestReferenceSettingsLocalFunctionsAndDefaults(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
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
					referenceRunStep(t, p.ctx, e, backend.record, s)
				}
				run(c, referenceExec("SET application_name='b'", 0))
				tx, err := c.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(tx, localReferenceSelect("SELECT set_config('application_name','s',false)", "set_config", "s"))
				if equalDefault {
					run(tx, referenceSelect("SELECT set_config('application_name',$1,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{d}}, d))
				} else {
					run(tx, referenceSelect("SELECT set_config('application_name',NULL,true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{d}}))
				}
				run(tx, referenceExec("RESET ALL", 0))
				run(tx, localReferenceSelect("SELECT current_setting('application_name')", "current_setting", d))
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
				run(c, localReferenceSelect("SHOW application_name", "application_name", want))
			}
		}
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, backend.record, referenceExec("SET new.rollback='x'", 0))
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, c, backend.record, localReferenceSelect("SHOW new.rollback", "new.rollback", ""))
		referenceRunStep(t, p.ctx, c, backend.record, referenceExec("RESET ALL", 0))
		referenceRunStep(t, p.ctx, c, backend.record, localReferenceSelect("SHOW new.rollback", "new.rollback", ""))
	}
}
