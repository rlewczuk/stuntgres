package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func spanishResourceOptions(t *testing.T) {
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY spanish_resource (TEMPLATE=snowball,Language='SpAnIsH',StopWords=spanish)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION spanish_resource_config (COPY=spanish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION spanish_resource_config ALTER MAPPING FOR asciiword,word WITH spanish_resource", 0)
		q := "SELECT ts_lexize('spanish_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, más, vector string }{
			{"stopwords", "language = 'SpAnIsH'", "{mas}", "'cas':2 'mas':1"},
			{"dummy", "language = 'SpAnIsH'", "{mas}", "'cas':2 'mas':1"},
			{"stopwords=''", "language = 'SpAnIsH', stopwords = ''", "{mas}", "'cas':2 'mas':1"},
			{"language=spanish,language='SpAnIsH'", "stopwords = '', language = 'SpAnIsH'", "{mas}", "'cas':2 'mas':1"},
			{"stopwords=spanish", "language = 'SpAnIsH', stopwords = 'spanish'", "{}", "'cas':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='spanish_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.más, "Más")
				dictionaryResult(t, p, b, e, q, "{cas}", "casas")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('spanish_resource_config','Más casas') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='spanish_resource'::regdictionary"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		fullSnapshot := spanishDefinitionSnapshot(t, p, b, c, "spanish_resource", "spanish_resource_config")
		preserved := func() {
			fullSnapshot()
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "Más")
			dictionaryResult(t, p, b, held, q, "{cas}", "casas")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='spanish_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='spanish_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('spanish_resource_config','Más casas') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Spanish resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=spanish,stopwords=spanish,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=spanish_bogus", "42704", `no Snowball stemmer available for language "spanish_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		spanishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_resource (language=english,stopwords=spanish_missing)")
		preserved()
	})
}
