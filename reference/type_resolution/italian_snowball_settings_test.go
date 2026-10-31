package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func italianConfigurationSettings(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, name := range []string{"ITALIAN", `pg_catalog."italian"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				italianSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.italian"}}))
				italianSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.italian"}}, name))
				italianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('i gatti e i cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'can':5 'gatt':2"}}))
			}
		}
	})
}
