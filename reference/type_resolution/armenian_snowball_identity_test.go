package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArmenianSnowballIdentity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dict, template, init, lexize, config int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE dictname='armenian_stem' AND dictnamespace='pg_catalog'::regnamespace").Scan(&dict, &template); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1::oid AND tmplname='snowball' AND tmplnamespace='pg_catalog'::regnamespace", template).Scan(&init, &lexize); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='armenian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&config); err != nil {
			t.Fatal(err)
		}
		seen := map[int64]bool{}
		for _, id := range []int64{dict, template, init, lexize, config} {
			if id <= 0 || seen[id] {
				t.Fatal("allocated Snowball identities", dict, template, init, lexize, config)
			}
			seen[id] = true
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner=(SELECT cfgowner FROM pg_ts_config WHERE oid=$2::oid) AS same_owner FROM pg_ts_dict WHERE oid=$1::oid", []string{"dictinitoption", "same_owner"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'armenian'", true}}, dict, config)
		regtsSelect(t, p, b, c, "SELECT dictnamespace::bigint,dictowner::bigint,dicttemplate=(SELECT dicttemplate FROM pg_ts_dict WHERE oid='english_stem'::regdictionary) AS shared FROM pg_ts_dict WHERE oid=$1::oid", []string{"dictnamespace", "dictowner", "shared"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(11), int64(10), true}}, dict)
		regtsSelect(t, p, b, c, "SELECT cfgnamespace::bigint,cfgowner::bigint FROM pg_ts_config WHERE oid=$1::oid", []string{"cfgnamespace", "cfgowner"}, []string{"INT8", "INT8"}, [][]any{{int64(11), int64(10)}}, config)
		for _, tc := range []struct {
			id         int64
			name, args string
			count      int64
		}{{init, "dsnowball_init", "2281", 1}, {lexize, "dsnowball_lexize", "2281 2281 2281 2281", 4}} {
			regtsSelect(t, p, b, c, "SELECT proname,proargtypes::text AS args,pronargs::bigint AS nargs,prorettype::bigint,proisstrict,provolatile,proparallel,prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS c_language FROM pg_proc WHERE oid=$1::oid", []string{"proname", "args", "nargs", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "probin", "c_language"}, []string{"NAME", "TEXT", "INT8", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "BOOL"}, [][]any{{tc.name, tc.args, tc.count, int64(2281), true, "v", "u", tc.name, "$libdir/dict_snowball", true}}, tc.id)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid=$1::oid AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, dict)
		regtsSelect(t, p, b, c, "SELECT cfgparser::bigint FROM pg_ts_config WHERE oid=$1::oid", []string{"cfgparser"}, []string{"INT8"}, [][]any{{int64(3722)}}, config)
	})
}

func TestReferenceArmenianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY armenian_prepared (TEMPLATE=snowball,Language=armenian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='armenian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('armenian_prepared'::regdictionary,'RATS'::text) AS lexemes"
		parameterSQL := "SELECT ts_lexize($1,$2) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		ph := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		regtsExec(t, p, b, c, "PREPARE armenian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE armenian_named", 0)
		regtsExec(t, p, b, c, "PREPARE armenian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE armenian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{rats}")
			dictionaryResult(t, p, b, c, "EXECUTE armenian_named", "{rats}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "armenian_prepared", "RATS")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY armenian_prepared (language=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{rats}")
			dictionaryResult(t, p, b, c, "EXECUTE armenian_named", "{rats}")
			dictionaryResult(t, p, b, c, constantSQL, "{rat}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{rat}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "armenian_prepared", "RATS")
			dictionaryResult(t, p, b, c, "EXECUTE armenian_params('armenian_prepared','RATS')", "{rat}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", []byte("armenian_prepared"), []byte("Rats"))
		}
		check()
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY armenian_prepared (stopwords=english,language=armenian_bogus)", "42704", `no Snowball stemmer available for language "armenian_bogus" and encoding "UTF8"`)
		armenianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY armenian_prepared (language=armenian,stopwords=armenian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='armenian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'english'"}})
		check()
	})
}
