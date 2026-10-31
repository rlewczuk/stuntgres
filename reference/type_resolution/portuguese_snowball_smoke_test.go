package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePortugueseSnowballFeatureSmoke(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"CASAS", "{cas}"}, {"corações", "{coraçõ}"}, {"não", "{}"}} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('portuguese_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		portugueseSmokeBoth(t, p, b, c, portugueseDebugStep())
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('portuguese','corações')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "corações", "{portuguese_stem}", "portuguese_stem", "{coraçõ}"}}))
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('portuguese','as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'coraçõ':4"}}))
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS maps,count(*) FILTER (WHERE mapdict='portuguese_stem'::regdictionary AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1) AS own FROM pg_ts_config_map WHERE mapcfg='portuguese'::regconfig", []string{"maps", "own"}, []string{"INT8", "INT8"}, [][]any{{int64(19), int64(6)}}))
	})
}
