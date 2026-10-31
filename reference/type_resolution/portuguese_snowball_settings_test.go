package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func portugueseConfigurationSettings(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, name := range []string{"portuguese", "PORTUGUESE", "pg_catalog.portuguese", `pg_catalog."portuguese"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.portuguese"}}))
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.portuguese"}}, name))
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'coraçõ':4"}}))
			}
		}
	})
}
