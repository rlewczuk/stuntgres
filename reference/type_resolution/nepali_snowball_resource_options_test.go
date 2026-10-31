package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func nepaliResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY nepali_resource (TEMPLATE=snowball,Language='NePaLi',StopWords=nepali)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION nepali_resource_config (COPY=nepali)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_resource_config ALTER MAPPING FOR asciiword,word WITH nepali_resource", 0)
		q := "SELECT ts_lexize('nepali_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, stopword, vector string }{
			{"stopwords", "language = 'NePaLi'", "{सबै}", "'घर':2 'सबै':1"},
			{"dummy", "language = 'NePaLi'", "{सबै}", "'घर':2 'सबै':1"},
			{"stopwords=''", "language = 'NePaLi', stopwords = ''", "{सबै}", "'घर':2 'सबै':1"},
			{"language=nepali,language='NePaLi'", "stopwords = '', language = 'NePaLi'", "{सबै}", "'घर':2 'सबै':1"},
			{"stopwords=nepali", "language = 'NePaLi', stopwords = 'nepali'", "{}", "'घर':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='nepali_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.stopword, "सबै")
				dictionaryResult(t, p, b, e, q, "{घर}", "घरहरू")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('nepali_resource_config','सबै घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='nepali_resource'::regdictionary"
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
			dictionaryResult(t, p, b, held, q, "{}", "सबै")
			dictionaryResult(t, p, b, held, q, "{घर}", "घरहरू")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='nepali_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='nepali_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('nepali_resource_config','सबै घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'घर':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Nepali resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=nepali,stopwords=nepali,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=nepali_bogus", "42704", `no Snowball stemmer available for language "nepali_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		nepaliMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_resource (language=english,stopwords=nepali_missing)")
		preserved()
	})
}
