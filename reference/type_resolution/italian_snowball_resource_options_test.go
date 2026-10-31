package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func italianResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY italian_resource (TEMPLATE=snowball,Language='ItAlIaN',StopWords=italian)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION italian_resource_config (COPY=italian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION italian_resource_config ALTER MAPPING FOR asciiword,word WITH italian_resource", 0)
		q := "SELECT ts_lexize('italian_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, perché, vector string }{
			{"stopwords", "language = 'ItAlIaN'", "{perc}", "'can':2 'perc':1"},
			{"dummy", "language = 'ItAlIaN'", "{perc}", "'can':2 'perc':1"},
			{"stopwords=''", "language = 'ItAlIaN', stopwords = ''", "{perc}", "'can':2 'perc':1"},
			{"language=italian,language='ItAlIaN'", "stopwords = '', language = 'ItAlIaN'", "{perc}", "'can':2 'perc':1"},
			{"stopwords=italian", "language = 'ItAlIaN', stopwords = 'italian'", "{}", "'can':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='italian_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.perché, "PERCHé")
				dictionaryResult(t, p, b, e, q, "{can}", "cani")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('italian_resource_config','PERCHé cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='italian_resource'::regdictionary"
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
			dictionaryResult(t, p, b, held, q, "{}", "PERCHé")
			dictionaryResult(t, p, b, held, q, "{can}", "cani")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='italian_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='italian_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('italian_resource_config','PERCHé cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'can':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Italian resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=italian,stopwords=italian,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=italian_bogus", "42704", `no Snowball stemmer available for language "italian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		italianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_resource (language=english,stopwords=italian_missing)")
		preserved()
	})
}
