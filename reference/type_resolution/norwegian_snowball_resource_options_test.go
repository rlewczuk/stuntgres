package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func norwegianResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY norwegian_resource (TEMPLATE=snowball,Language='NoRwEgIaN',StopWords=norwegian)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION norwegian_resource_config (COPY=norwegian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION norwegian_resource_config ALTER MAPPING FOR asciiword,word WITH norwegian_resource", 0)
		q := "SELECT ts_lexize('norwegian_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, ikke, vector string }{
			{"stopwords", "language = 'NoRwEgIaN'", "{ikk}", "'bil':2 'ikk':1"},
			{"dummy", "language = 'NoRwEgIaN'", "{ikk}", "'bil':2 'ikk':1"},
			{"stopwords=''", "language = 'NoRwEgIaN', stopwords = ''", "{ikk}", "'bil':2 'ikk':1"},
			{"language=norwegian,language='NoRwEgIaN'", "stopwords = '', language = 'NoRwEgIaN'", "{ikk}", "'bil':2 'ikk':1"},
			{"stopwords=norwegian", "language = 'NoRwEgIaN', stopwords = 'norwegian'", "{}", "'bil':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='norwegian_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.ikke, "IKKE")
				dictionaryResult(t, p, b, e, q, "{bil}", "biler")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('norwegian_resource_config','IKKE biler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='norwegian_resource'::regdictionary"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		fullSnapshot := norwegianDefinitionSnapshot(t, p, b, c, "norwegian_resource", "norwegian_resource_config")
		preserved := func() {
			fullSnapshot()
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "IKKE")
			dictionaryResult(t, p, b, held, q, "{bil}", "biler")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='norwegian_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='norwegian_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('norwegian_resource_config','IKKE biler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Norwegian resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=norwegian,stopwords=norwegian,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=norwegian_bogus", "42704", `no Snowball stemmer available for language "norwegian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		norwegianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_resource (language=english,stopwords=norwegian_missing)")
		preserved()
	})
}
