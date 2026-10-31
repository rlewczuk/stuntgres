package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePorterSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterAbsentStock(t, p, b, c)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY porter_smoke (TEMPLATE=pg_catalog.snowball,Language=porter,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION porter_smoke_config (COPY=pg_catalog.simple)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION porter_smoke_config ALTER MAPPING FOR asciiword WITH porter_smoke", 0)
		for _, tc := range [][2]string{{"SKIES", "{ski}"}, {"ONLY", "{}"}} {
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('porter_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('porter_smoke_config','SKIES')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "SKIES", "{porter_smoke}", "porter_smoke", "{ski}"}}))
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_smoke_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2"}}))
		regtsSelect(t, p, b, c, "SELECT dictnamespace='public'::regnamespace AS namespace,dicttemplate=(SELECT oid FROM pg_ts_template WHERE tmplname='snowball' AND tmplnamespace='pg_catalog'::regnamespace) AS genuine FROM pg_ts_dict WHERE oid='porter_smoke'::regdictionary", []string{"namespace", "genuine"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_smoke (stopwords)", 0)
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('porter_smoke','ONLY') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{onli}"}}))
		porterAbsentStock(t, p, b, c)
	})
}
