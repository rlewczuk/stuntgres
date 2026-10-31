package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func spanishConfigurationSettings(t *testing.T) {
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, name := range []string{"spanish", "SPANISH", "pg_catalog.spanish", `pg_catalog."spanish"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.spanish"}}))
				spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.spanish"}}, name))
				spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('las casas y niños') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'niñ':4"}}))
			}
		}
	})
}
