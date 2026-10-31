package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTurkishSnowballIdentity(t *testing.T) {
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dict, template, init, lexize, config int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE dictname='turkish_stem' AND dictnamespace='pg_catalog'::regnamespace").Scan(&dict, &template); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1::oid AND tmplname='snowball' AND tmplnamespace='pg_catalog'::regnamespace", template).Scan(&init, &lexize); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='turkish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&config); err != nil {
			t.Fatal(err)
		}
		seen := map[int64]bool{}
		for _, id := range []int64{dict, template, init, lexize, config} {
			if id <= 0 || seen[id] {
				t.Fatal("allocated Snowball identities", dict, template, init, lexize, config)
			}
			seen[id] = true
		}
		for _, name := range []string{"simple", "english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese"} {
			dictionaryName := name + "_stem"
			if name == "simple" {
				dictionaryName = name
			}
			var earlierDict, earlierConfig int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint,$2::regconfig::oid::bigint", dictionaryName, name).Scan(&earlierDict, &earlierConfig); err != nil {
				t.Fatal(err)
			}
			if earlierDict <= 0 || earlierConfig <= 0 || earlierDict == dict || earlierConfig == config {
				t.Fatal("Turkish identity aliases an earlier seed", name, earlierDict, earlierConfig, dict, config)
			}
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner=(SELECT cfgowner FROM pg_ts_config WHERE oid=$2::oid) AS same_owner FROM pg_ts_dict WHERE oid=$1::oid", []string{"dictinitoption", "same_owner"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'turkish', stopwords = 'turkish'", true}}, dict, config)
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
		for _, object := range []struct{ class, id int64 }{{3764, template}, {3602, config}, {1255, init}, {1255, lexize}} {
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=$1::oid AND objid=$2::oid AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, object.class, object.id)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY turkish_custom_identity (TEMPLATE=pg_catalog.snowball,Language=turkish)", 0)
		regtsSelect(t, p, b, c, "SELECT oid<>$1::oid AS distinct_dictionary,dicttemplate=$2::oid AS shared_template,dictnamespace='public'::regnamespace AS user_namespace,dictinitoption FROM pg_ts_dict WHERE oid='turkish_custom_identity'::regdictionary", []string{"distinct_dictionary", "shared_template", "user_namespace", "dictinitoption"}, []string{"BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, "language = 'turkish'"}}, dict, template)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('turkish_custom_identity','biz') AS lexemes", "{biz}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('turkish_stem','biz') AS lexemes", "{}")
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT count(*) AS n FROM pg_ts_template WHERE tmplname='snowball' AND tmplnamespace=11", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace=11 AND proname IN ('dsnowball_init','dsnowball_lexize')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
