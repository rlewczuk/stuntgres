package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTextSearchSettingSession(t *testing.T) {
	for _, f := range textSearchSettingForms() {
		t.Run(f.name, func(t *testing.T) {
			textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer peer.Close()
				defer ftsConvertRestore(t, p, peer)()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				var baseline string
				if err := a.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&baseline); err != nil { t.Fatal(err) }
				// Capture which pinned-fixture baseline is active; its outcomes
				// still come from the independent inventory, never queried goldens.
				baselineEnglish := baseline == "pg_catalog.english"
				if !baselineEnglish && baseline != "pg_catalog.simple" {
					t.Fatalf("unaccounted fixture reset configuration %q", baseline)
				}
				run(fixture.Exec("SET default_text_search_config='simple'", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET default_text_search_config='english'", 0))
				for _, parameter := range []bool{false, true} {
					q, args := f.query(parameter, false, nil, f.input(), textSearchSettingFilter(f))
					h := textSearchSettingHold(t, p, a, q)
					defer h.Statement.Close()
					check := func(english bool) {
						textSearchSettingRoutes(t, p, b, a, h, f.step(q, english, false, args...))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, f.step(q, true, false, args...))
					}
					check(false)
					for _, end := range []string{"COMMIT", "ROLLBACK"} {
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
						check(true)
						run(fixture.Exec(end, 0))
						check(false)
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Select("SELECT set_config('default_text_search_config','english',true) AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{"pg_catalog.english"}}))
					check(true)
					run(fixture.Exec("COMMIT", 0))
					check(false)
					for _, local := range []string{"", "LOCAL "} {
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
						run(fixture.Exec("SAVEPOINT selection", 0))
						run(fixture.Exec("SET "+local+"default_text_search_config='simple'", 0))
						check(false)
						run(fixture.Exec("ROLLBACK TO SAVEPOINT selection", 0))
						check(true)
						run(fixture.Exec("ROLLBACK", 0))
						check(false)
					}
					run(fixture.Select("SELECT set_config('default_text_search_config',NULL,false) AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{baseline}}))
					run(textSearchSettingShow(baseline))
					textSearchSettingRoutes(t, p, b, a, h, f.step(q, baselineEnglish, false, args...))
					run(fixture.Exec("SET default_text_search_config='simple'", 0))
				}
			})
		})
	}
}
