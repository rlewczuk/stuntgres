package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func turkishConfigurationSettings(t *testing.T) {
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("SET default_text_search_config TO TURKISH", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.turkish"}}))
		}
		for _, name := range []string{"turkish", "TURKISH", "pg_catalog.turkish", `pg_catalog."turkish"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.turkish"}}))
				turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.turkish"}}, name))
				turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('biz kitaplar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'kitap':2"}}))
			}
		}
	})
}
