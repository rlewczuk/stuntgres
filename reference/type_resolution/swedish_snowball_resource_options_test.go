package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func swedishResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY swedish_resource (TEMPLATE=snowball,Language='SwEdIsH',StopWords=swedish)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION swedish_resource_config (COPY=swedish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION swedish_resource_config ALTER MAPPING FOR asciiword,word WITH swedish_resource", 0)
		q := "SELECT ts_lexize('swedish_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, sitta, vector string }{
			{"stopwords", "language = 'SwEdIsH'", "{sitt}", "'hund':2 'sitt':1"},
			{"dummy", "language = 'SwEdIsH'", "{sitt}", "'hund':2 'sitt':1"},
			{"stopwords=''", "language = 'SwEdIsH', stopwords = ''", "{sitt}", "'hund':2 'sitt':1"},
			{"language=swedish,language='SwEdIsH'", "stopwords = '', language = 'SwEdIsH'", "{sitt}", "'hund':2 'sitt':1"},
			{"stopwords=swedish", "language = 'SwEdIsH', stopwords = 'swedish'", "{}", "'hund':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='swedish_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.sitta, "SITTA")
				dictionaryResult(t, p, b, e, q, "{hund}", "hundar")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('swedish_resource_config','SITTA hundar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='swedish_resource'::regdictionary"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		fullSnapshot := norwegianDefinitionSnapshot(t, p, b, c, "swedish_resource", "swedish_resource_config")
		preserved := func() {
			fullSnapshot()
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "SITTA")
			dictionaryResult(t, p, b, held, q, "{hund}", "hundar")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='swedish_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='swedish_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('swedish_resource_config','SITTA hundar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Swedish resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=swedish,stopwords=swedish,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=swedish_bogus", "42704", `no Snowball stemmer available for language "swedish_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		swedishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_resource (language=english,stopwords=swedish_missing)")
		preserved()
	})
}
