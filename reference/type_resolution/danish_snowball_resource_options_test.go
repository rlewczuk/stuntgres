package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func danishResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY danish_resource (TEMPLATE=snowball,Language='DaNiSh',StopWords=danish)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION danish_resource_config (COPY=danish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION danish_resource_config ALTER MAPPING FOR asciiword WITH danish_resource", 0)
		q := "SELECT ts_lexize('danish_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, og, vector string }{
			{"stopwords", "language = 'DaNiSh'", "{og}", "'hund':2 'og':1"},
			{"dummy", "language = 'DaNiSh'", "{og}", "'hund':2 'og':1"},
			{"stopwords=''", "language = 'DaNiSh', stopwords = ''", "{og}", "'hund':2 'og':1"},
			{"language=danish,language='DaNiSh'", "stopwords = '', language = 'DaNiSh'", "{og}", "'hund':2 'og':1"},
			{"stopwords=danish", "language = 'DaNiSh', stopwords = 'danish'", "{}", "'hund':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY danish_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='danish_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.og, "OG")
				dictionaryResult(t, p, b, e, q, "{hund}", "HUNDENE")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('danish_resource_config','OG HUNDENE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='danish_resource'::regdictionary"
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
			dictionaryResult(t, p, b, held, q, "{}", "OG")
			dictionaryResult(t, p, b, held, q, "{hund}", "HUNDENE")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='danish_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='danish_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('danish_resource_config','OG HUNDENE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Danish resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=danish,stopwords=danish,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=danish_bogus", "42704", `no Snowball stemmer available for language "danish_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY danish_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		danishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY danish_resource (language=english,stopwords=danish_missing)")
		preserved()
	})
}
