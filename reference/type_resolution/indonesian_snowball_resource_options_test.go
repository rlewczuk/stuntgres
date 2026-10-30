package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func indonesianResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY indonesian_resource (TEMPLATE=snowball,Language='InDoNeSiAn',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION indonesian_resource_config (COPY=indonesian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION indonesian_resource_config ALTER MAPPING FOR asciiword WITH indonesian_resource", 0)
		q := "SELECT ts_lexize('indonesian_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, the, vector string }{
			{"stopwords", "language = 'InDoNeSiAn'", "{the}", "'sapu':2 'the':1"},
			{"dummy", "language = 'InDoNeSiAn'", "{the}", "'sapu':2 'the':1"},
			{"stopwords=''", "language = 'InDoNeSiAn', stopwords = ''", "{the}", "'sapu':2 'the':1"},
			{"language=indonesian,language='InDoNeSiAn'", "stopwords = '', language = 'InDoNeSiAn'", "{the}", "'sapu':2 'the':1"},
			{"stopwords=english", "language = 'InDoNeSiAn', stopwords = 'english'", "{}", "'sapu':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='indonesian_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.the, "THE")
				dictionaryResult(t, p, b, e, q, "{sapu}", "MENYAPU")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('indonesian_resource_config','THE MENYAPU') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='indonesian_resource'::regdictionary"
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
			dictionaryResult(t, p, b, held, q, "{}", "THE")
			dictionaryResult(t, p, b, held, q, "{sapu}", "MENYAPU")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='indonesian_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='indonesian_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('indonesian_resource_config','THE MENYAPU') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'sapu':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Indonesian resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=indonesian,stopwords=english,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=indonesian_bogus", "42704", `no Snowball stemmer available for language "indonesian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		indonesianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_resource (language=english,stopwords=indonesian)")
		preserved()
	})
}
