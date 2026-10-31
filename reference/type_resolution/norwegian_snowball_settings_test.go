package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func norwegianConfigurationSettings(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, name := range []string{"norwegian", "NORWEGIAN", "pg_catalog.norwegian", `pg_catalog."norwegian"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.norwegian"}}))
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.norwegian"}}, name))
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2 'båt':4"}}))
			}
		}
	})
}
