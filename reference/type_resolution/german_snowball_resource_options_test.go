package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func germanResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY german_resource (TEMPLATE=snowball,Language='GeRmAn',StopWords=german)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION german_resource_config (COPY=german)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION german_resource_config ALTER MAPPING FOR asciiword WITH german_resource", 0)
		q := "SELECT ts_lexize('german_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, und, vector string }{
			{"stopwords", "language = 'GeRmAn'", "{und}", "'haus':2 'und':1"},
			{"dummy", "language = 'GeRmAn'", "{und}", "'haus':2 'und':1"},
			{"stopwords=''", "language = 'GeRmAn', stopwords = ''", "{und}", "'haus':2 'und':1"},
			{"language=german,language='GeRmAn'", "stopwords = '', language = 'GeRmAn'", "{und}", "'haus':2 'und':1"},
			{"stopwords=german", "language = 'GeRmAn', stopwords = 'german'", "{}", "'haus':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY german_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='german_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.und, "UND")
				dictionaryResult(t, p, b, e, q, "{haus}", "HAUSER")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('german_resource_config','UND HAUSER') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='german_resource'::regdictionary"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		preserved := func() {
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "UND")
			dictionaryResult(t, p, b, held, q, "{haus}", "HAUSER")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='german_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='german_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('german_resource_config','UND HAUSER') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'haus':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed German resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=german,stopwords=german,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=german_bogus", "42704", `no Snowball stemmer available for language "german_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY german_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		germanMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY german_resource (language=english,stopwords=german_missing)")
		preserved()
	})
}
