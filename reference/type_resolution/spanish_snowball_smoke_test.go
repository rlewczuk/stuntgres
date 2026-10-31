package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSpanishSnowballFeatureSmoke(t *testing.T) {
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"CASAS", "{cas}"}, {"niños", "{niñ}"}, {"más", "{}"}, {"educación", "{educ}"}, {"educacion", "{educacion}"}} {
			spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('spanish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		spanishSmokeBoth(t, p, b, c, spanishDebugStep())
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('spanish','niños')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "niños", "{spanish_stem}", "spanish_stem", "{niñ}"}}))
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('spanish','las casas y niños') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'niñ':4"}}))
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS maps,count(*) FILTER (WHERE mapdict='spanish_stem'::regdictionary AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1) AS own FROM pg_ts_config_map WHERE mapcfg='spanish'::regconfig", []string{"maps", "own"}, []string{"INT8", "INT8"}, [][]any{{int64(19), int64(6)}}))
	})
}
