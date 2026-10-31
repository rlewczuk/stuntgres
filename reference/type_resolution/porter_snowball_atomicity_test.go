package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePorterSnowballAtomicity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterSetup(t, p, b, c, "porter_atomic", "porter_atomic_config", true)
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "porter_atomic", "porter_atomic_config")
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE TEXT SEARCH DICTIONARY porter_failed (TEMPLATE=snowball,Language=porter,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`},
			{"ALTER TEXT SEARCH DICTIONARY porter_atomic (language=english,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`},
			{"ALTER TEXT SEARCH DICTIONARY porter_atomic (language)", "22023", "missing Language parameter"},
			{"ALTER TEXT SEARCH CONFIGURATION porter_atomic_config ALTER MAPPING FOR asciiword,porter_missing_alias WITH porter_atomic", "22023", `token type "porter_missing_alias" does not exist`},
		} {
			dictionaryError(t, p, b, c, tc.q, tc.code, tc.message)
			preserved()
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('porter_atomic',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ski}"}}, "SKIES"))
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('porter_atomic','ONLY') AS lexemes", "{}")
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_atomic_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2"}}))
		}
		porterMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_atomic (language=english,stopwords=porter_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='porter_failed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
