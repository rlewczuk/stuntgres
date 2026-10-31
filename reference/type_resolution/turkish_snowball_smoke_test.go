package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTurkishSnowballFeatureSmoke(t *testing.T) {
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"KITAPLAR", "{kitap}"}, {"ağacı", "{ağaç}"}, {"biz", "{}"}, {"o'connor", "{o}"}} {
			turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('turkish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		turkishSmokeBoth(t, p, b, c, turkishDebugStep())
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('turkish','ağacı')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "ağacı", "{turkish_stem}", "turkish_stem", "{ağaç}"}}))
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('turkish','ve kitaplar çok evler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ev':4 'kitap':2"}}))
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS maps,count(*) FILTER (WHERE mapdict='turkish_stem'::regdictionary AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1) AS own,count(*) FILTER (WHERE mapdict='simple'::regdictionary AND maptokentype IN (3,4,5,6,7,8,9,15,18,19,20,21,22) AND mapseqno=1) AS simple FROM pg_ts_config_map WHERE mapcfg='turkish'::regconfig", []string{"maps", "own", "simple"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(19), int64(6), int64(13)}}))
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT pg_typeof(ts_lexize('turkish_stem','kitaplar'))::oid::bigint AS lexize_oid,pg_typeof(to_tsvector('turkish','ve kitaplar çok evler'))::oid::bigint AS vector_oid", []string{"lexize_oid", "vector_oid"}, []string{"INT8", "INT8"}, [][]any{{int64(1009), int64(3614)}}))
	})
}
