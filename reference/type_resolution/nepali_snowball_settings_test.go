package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func nepaliConfigurationSettings(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer regtsExec(t, p, b, c, "RESET default_text_search_config", 0)
		for _, name := range []string{"NEPALI", `pg_catalog."nepali"`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET default_text_search_config TO '"+name+"'", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.nepali"}}))
				nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT set_config('default_text_search_config',$1::text,false) AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.nepali"}}, name))
				nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('RATS र घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1 'घर':3"}}))
			}
		}
		for _, missing := range []string{`pg_catalog."Nepali"`, "nepali_missing"} {
			dictionaryError(t, p, b, c, "SET default_text_search_config TO '"+missing+"'", "22023", `invalid value for parameter "default_text_search_config": "`+missing+`"`)
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT current_setting('default_text_search_config') AS current", []string{"current"}, []string{"TEXT"}, [][]any{{"pg_catalog.nepali"}}))
		}
	})
}
func TestReferenceNepaliSnowballSettings(t *testing.T) { nepaliConfigurationSettings(t) }
