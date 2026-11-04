package reference_test

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsFractionalLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fractional_role"})
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				a, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				b, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				owner, peer := fixture.SettingsPhysical(t, a), fixture.SettingsPhysical(t, b)
				run := func(s fixture.Step) {
					t.Helper()
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
				}
				control := func(q string) { t.Helper(); fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec(q, 0)) }
				show := func(want string) {
					t.Helper()
					for _, s := range settingsFractionalShow(backend.PG, "random_page_cost", want) {
						run(s)
					}
				}
				run(fixture.Exec("SET random_page_cost TO '3'", 0))
				control("BEGIN")
				run(fixture.Exec("SET random_page_cost = 1.5", 0))
				show("1.5")
				control("ROLLBACK")
				show("3")
				control("BEGIN")
				run(fixture.Exec("SET random_page_cost = 1.5", 0))
				control("COMMIT")
				show("1.5")
				control("BEGIN")
				control("SAVEPOINT s")
				run(fixture.Exec("SET random_page_cost TO .25", 0))
				show("0.25")
				control("ROLLBACK TO SAVEPOINT s")
				show("1.5")
				run(fixture.Exec("SET LOCAL random_page_cost = 2.5e0", 0))
				show("2.5")
				control("COMMIT")
				show("1.5")
				// A rejected value aborts the transaction; nothing is published.
				control("BEGIN")
				run(settingsReferenceError("SET random_page_cost = -1.5", "22023", `-1.5 is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
				aborted := fixture.ErrorDiagnostic("SHOW random_page_cost", fixture.TransactionAborted())
				aborted.ErrorStage = "execute"
				if prepared {
					aborted.ErrorStage = "prepare"
				}
				run(aborted)
				control("ROLLBACK")
				show("1.5")
				// Sessions are isolated; placeholders exist only where assigned.
				run(fixture.Exec("SET fractional.rate = 0.5", 0))
				peerShow := localReferenceSelect(backend.PG, "SHOW random_page_cost", "random_page_cost", "4")
				peerShow.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b, backend.Recorder, peerShow)
				peerMissing := settingsReferenceError("SHOW fractional.rate", "42704", `unrecognized configuration parameter "fractional.rate"`, "", "")
				peerMissing.Prepared = prepared
				if prepared {
					peerMissing.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b, backend.Recorder, peerMissing)
				run(fixture.Exec("RESET random_page_cost", 0))
				show("4")
				run(fixture.Exec("SET random_page_cost = 1.5", 0))
				run(fixture.Exec("SET random_page_cost TO DEFAULT", 0))
				show("4")

				// Held prepared SET changes state at execution, each time it runs.
				run(fixture.Exec("SET random_page_cost TO '3'", 0))
				held, err := a.PrepareContext(p.Ctx, "SET random_page_cost = 1.25")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				rejected, err := a.PrepareContext(p.Ctx, "SET random_page_cost = -1.25")
				if err != nil {
					t.Fatal(err)
				}
				defer rejected.Close()
				show("3")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: "SET random_page_cost = 1.25"}, backend.Recorder, fixture.Exec("SET random_page_cost = 1.25", 0))
				show("1.25")
				run(fixture.Exec("SET random_page_cost TO '3'", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: rejected, SQL: "SET random_page_cost = -1.25"}, backend.Recorder, settingsReferenceError("SET random_page_cost = -1.25", "22023", `-1.25 is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
				show("3")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: "SET random_page_cost = 1.25"}, backend.Recorder, fixture.Exec("SET random_page_cost = 1.25", 0))
				show("1.25")

				// Routine SET clauses store and apply the flattened text.
				run(fixture.Exec("CREATE FUNCTION fractional_f() RETURNS text LANGUAGE sql SET random_page_cost = 2.5 SET fractional.rate = -.5 AS $$SELECT current_setting('random_page_cost') || '/' || current_setting('fractional.rate')$$", 0))
				run(fixture.Select("SELECT fractional_f()", []string{"fractional_f"}, []string{"TEXT"}, [][]any{{"2.5/-.5"}}))
				run(fixture.Select("SELECT array_to_string(proconfig, ';') FROM pg_proc WHERE proname = 'fractional_f'", []string{"array_to_string"}, []string{"TEXT"}, [][]any{{"random_page_cost=2.5;fractional.rate=-.5"}}))
				show("1.25")
				run(localReferenceSelect(backend.PG, "SHOW fractional.rate", "fractional.rate", "0.5"))
				run(settingsReferenceError("CREATE FUNCTION fractional_g() RETURNS int LANGUAGE sql SET random_page_cost = -1.5 AS $$SELECT 1$$", "22023", `-1.5 is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
				run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname = 'fractional_g'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("DROP FUNCTION fractional_f", 0))

				// Stored role defaults retain the flattened text after validation.
				roleSettings := fixture.Select("SELECT array_to_string(setconfig, ';') FROM pg_db_role_setting s JOIN pg_roles r ON r.oid = s.setrole WHERE r.rolname = 'fractional_role'", []string{"array_to_string"}, []string{"TEXT"}, [][]any{{"random_page_cost=1.5;fractional.rate=-.5;statement_timeout=1.5e3"}})
				run(fixture.Exec("CREATE ROLE fractional_role", 0))
				run(fixture.Exec("ALTER ROLE fractional_role SET random_page_cost = 1.5", 0))
				run(fixture.Exec("ALTER ROLE fractional_role SET fractional.rate = -.5", 0))
				run(fixture.Exec("ALTER ROLE fractional_role SET statement_timeout TO 1.5e3", 0))
				run(roleSettings)
				run(settingsReferenceError("ALTER ROLE fractional_role SET random_page_cost = -1.5", "22023", `-1.5 is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
				run(roleSettings)
				run(fixture.Exec("DROP ROLE fractional_role", 0))
				// Database defaults: filter to this test's entries on the shared database.
				var database string
				if err := a.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				alter := `ALTER DATABASE "` + strings.ReplaceAll(database, `"`, `""`) + `"`
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{alter + " RESET fractional.rate", alter + " RESET random_page_cost"} {
						if _, err := backend.DB.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				})
				databaseSettings := fixture.Select("SELECT string_agg(c, ';' ORDER BY o) FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase, unnest(s.setconfig) WITH ORDINALITY u(c, o) WHERE d.datname = current_database() AND s.setrole = 0 AND (c LIKE 'fractional.%' OR c LIKE 'random_page_cost=%')", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"fractional.rate=-.5;random_page_cost=.75"}})
				run(fixture.Exec(alter+" SET fractional.rate = -.5", 0))
				run(fixture.Exec(alter+" SET random_page_cost TO .75", 0))
				run(databaseSettings)
				run(settingsReferenceError(alter+" SET random_page_cost = -.75", "22023", `-0.75 is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
				run(databaseSettings)
				run(fixture.Exec(alter+" RESET fractional.rate", 0))
				run(fixture.Exec(alter+" RESET random_page_cost", 0))
				run(fixture.Exec("RESET ALL", 0))
				if fixture.SettingsPhysical(t, a) != owner || fixture.SettingsPhysical(t, b) != peer {
					t.Fatal("fractional settings replaced a physical session")
				}
			}
		})
	}
}

// Public smoke: one connection assigns PostgreSQL-spelled fractional values to
// a placeholder, a real and a unit-bearing integer setting, then observes them.
func TestReferenceSettingsFractionalFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
		run(fixture.Exec("SET smoke_ext.sample_rate = 0.5", 0))
		run(fixture.Exec("SET random_page_cost TO 1.1", 0))
		run(fixture.Exec("SET SESSION lock_timeout = 1.5e3", 0))
		run(fixture.Select("SELECT current_setting('smoke_ext.sample_rate'), current_setting('random_page_cost'), current_setting('lock_timeout')", []string{"current_setting", "current_setting", "current_setting"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"0.5", "1.1", "1500ms"}}))
		run(fixture.Select("SELECT name, setting, unit FROM pg_settings WHERE name IN ('random_page_cost', 'lock_timeout') ORDER BY name", []string{"name", "setting", "unit"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"lock_timeout", "1500", "ms"}, {"random_page_cost", "1.1", nil}}))
		run(fixture.Exec("RESET ALL", 0))
		run(fixture.Select("SELECT current_setting('smoke_ext.sample_rate'), current_setting('random_page_cost'), current_setting('lock_timeout')", []string{"current_setting", "current_setting", "current_setting"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"", "4", "0"}}))
	}
}
