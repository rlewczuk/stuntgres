package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsCallOutputLabelsEffects(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
				runOwner := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
				var capturedDefault string
				if err := owner.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&capturedDefault); err != nil {
					t.Fatal(err)
				}
				runOwner(fixture.Exec("SET application_name='label-base'", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
				query := `SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`
				for _, sql := range []string{
					`SELECT pg_catalog.set_config('application_name','prepare-lost',false) AS value`,
					query,
					`SELECT pg_catalog.set_config('label_prepare.unseen','prepare-lost',false) value`,
				} {
					stmt, err := owner.PrepareContext(p.Ctx, sql)
					if err != nil {
						t.Fatal(err)
					}
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					settingsLabelState(t, p, b, owner, peer, "label-base", "label-peer")
					runOwner(settingsLabelValue(b.PG, `SELECT current_setting('label_prepare.unseen',true) value`, "value", nil))
				}
				run := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, query)
				set := func(value any, local any, want string) {
					run(settingsLabelValue(b.PG, query, "SetValue", want, "application_name", value, local))
				}
				set("label-session", false, "label-session")
				settingsLabelState(t, p, b, owner, peer, "label-session", "label-peer")
				set(nil, nil, capturedDefault)
				settingsLabelState(t, p, b, owner, peer, capturedDefault, "label-peer")
				for _, finish := range []string{"COMMIT", "ROLLBACK"} {
					for _, sessionFirst := range []bool{false, true} {
						runOwner(fixture.Exec("SET application_name='label-base'", 0))
						runOwner(fixture.Exec("BEGIN", 0))
						// Ensure a fatal assertion cannot leave an open transaction.
						func() {
							finished := false
							defer func() {
								if !finished {
									owner.ExecContext(p.Ctx, "ROLLBACK")
								}
							}()
							if sessionFirst {
								set("label-session", false, "label-session")
							}
							set("label-local", true, "label-local")
							settingsLabelState(t, p, b, owner, peer, "label-local", "label-peer")
							runOwner(fixture.Exec("SAVEPOINT label_sp", 0))
							set("label-savepoint", true, "label-savepoint")
							settingsLabelState(t, p, b, owner, peer, "label-savepoint", "label-peer")
							runOwner(fixture.Exec("ROLLBACK TO SAVEPOINT label_sp", 0))
							settingsLabelState(t, p, b, owner, peer, "label-local", "label-peer")
							runOwner(fixture.Exec(finish, 0))
							finished = true
							want := "label-base"
							if sessionFirst && finish == "COMMIT" {
								want = "label-session"
							}
							settingsLabelState(t, p, b, owner, peer, want, "label-peer")
						}()
					}
				}
				runOwner(fixture.Exec("SET application_name='label-base'", 0))
				set("label-transient", true, "label-transient")
				settingsLabelState(t, p, b, owner, peer, "label-base", "label-peer")
			})
		})
	}
	// Notice-enabled owners prove Prepare silence and exactly one real
	// normalization notice per execution, independent of callback timing.
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run("notices/"+route, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, owner, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
					run(fixture.Exec("SET application_name='label-base'", 0))
					run(fixture.Exec("SET client_min_messages=notice", 0))
					original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
					query := `SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`
					before := len(notices)
					for _, q := range []string{query, `SELECT pg_catalog.set_config('application_name','` + original + `',false) AS value`, `SELECT set_config('label_notice.unseen','x',false) value`} {
						stmt, err := owner.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if len(fixture.NoticeWitnesses(t, backend.PG, notices[before:], want)) != 0 {
						t.Fatal("Prepare emitted notices", notices[before:])
					}
					run(settingsLabelValue(backend.PG, "SHOW application_name", "application_name", "label-base"))
					run(settingsLabelValue(backend.PG, `SELECT current_setting('label_notice.unseen',true) value`, "value", nil))
					execute := settingsLabelRoute(t, p.Ctx, owner, recorder, route, query)
					before = len(notices)
					execute(settingsLabelValue(backend.PG, query, "SetValue", clipped, "application_name", original, false))
					if got := fixture.NoticeWitnesses(t, backend.PG, notices[before:], want); len(got) != 1 || got[0] != want {
						t.Fatal("execution notice", got)
					}
					run(settingsLabelValue(backend.PG, "SHOW application_name", "application_name", clipped))
					before = len(notices)
					execute(settingsLabelValue(backend.PG, query, "SetValue", "label-transient", "application_name", "label-transient", true))
					if got := fixture.NoticeWitnesses(t, backend.PG, notices[before:], want); len(got) != 0 {
						t.Fatal("implicit local emitted notice", got)
					}
					run(settingsLabelValue(backend.PG, "SHOW application_name", "application_name", clipped))
				})
			}
		})
	}
}
