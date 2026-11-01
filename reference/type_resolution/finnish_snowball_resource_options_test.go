package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func finnishResourceOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY finnish_resource (TEMPLATE=snowball,Language='FiNnIsH',StopWords=finnish)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION finnish_resource_config (COPY=finnish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION finnish_resource_config ALTER MAPPING FOR asciiword WITH finnish_resource", 0)
		q := "SELECT ts_lexize('finnish_resource',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, on, vector string }{
			{"stopwords", "language = 'FiNnIsH'", "{on}", "'on':1 'talo':2"},
			{"dummy", "language = 'FiNnIsH'", "{on}", "'on':1 'talo':2"},
			{"stopwords=''", "language = 'FiNnIsH', stopwords = ''", "{on}", "'on':1 'talo':2"},
			{"language=finnish,language='FiNnIsH'", "stopwords = '', language = 'FiNnIsH'", "{on}", "'on':1 'talo':2"},
			{"stopwords=finnish", "language = 'FiNnIsH', stopwords = 'finnish'", "{}", "'talo':2"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_resource ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='finnish_resource'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.on, "ON")
				dictionaryResult(t, p, b, e, q, "{talo}", "TALOSSA")
			}
			regtsSelect(t, p, b, c, "SELECT to_tsvector('finnish_resource_config','ON TALOSSA') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.vector}})
		}
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='finnish_resource'::regdictionary"
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
			dictionaryResult(t, p, b, held, q, "{}", "ON")
			dictionaryResult(t, p, b, held, q, "{talo}", "TALOSSA")
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='finnish_resource'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='finnish_resource_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('finnish_resource_config','ON TALOSSA') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'talo':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed Finnish resource options published", err)
				}
			}
		}
		for _, tc := range []struct{ options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords='',accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"language=finnish,stopwords=finnish,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=finnish_bogus", "42704", `no Snowball stemmer available for language "finnish_bogus" and encoding "UTF8"`},
			{"stopwords='../finnish'", "22023", `invalid text search configuration file name "../finnish"`},
			{"stopwords='Finnish'", "22023", `invalid text search configuration file name "Finnish"`},
			{"stopwords='café'", "22023", `invalid text search configuration file name "café"`},
			{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
			{"language=' finnish '", "42704", `no Snowball stemmer available for language " finnish " and encoding "UTF8"`},
			{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
			{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_resource ("+tc.options+")", tc.code, tc.message)
			preserved()
		}
		finnishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_resource (language=english,stopwords=finnish_missing)")
		preserved()
	})
}
