package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePorterSnowballIdentity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterAbsentStock(t, p, b, c)
		porterSetup(t, p, b, c, "porter_identity", "porter_identity_config", false)
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'porter_identity'::regdictionary::oid::bigint").Scan(&id); err != nil || id <= 0 {
			t.Fatal(id, err)
		}
		porterSmokeBoth(t, p, b, c, fixture.Select(`SELECT d.oid::bigint,
   d.dictnamespace='public'::regnamespace AS namespace,
   d.dictowner=current_user::regrole::oid AS owned,d.dictinitoption,
   t.tmplname::text,n.nspname::text,
   t.tmplinit='pg_catalog.dsnowball_init(internal)'::regprocedure AS init,
   t.tmpllexize='pg_catalog.dsnowball_lexize(internal,internal,internal,internal)'::regprocedure AS lexize
   FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate
   JOIN pg_namespace n ON n.oid=t.tmplnamespace WHERE d.oid='porter_identity'::regdictionary`,
			[]string{"oid", "namespace", "owned", "dictinitoption", "tmplname", "nspname", "init", "lexize"},
			[]string{"INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"},
			[][]any{{id, true, true, "language = 'porter'", "snowball", "pg_catalog", true, true}}))
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT cfgnamespace='public'::regnamespace AS namespace,cfgowner=current_user::regrole::oid AS owned,cfgparser::bigint FROM pg_ts_config WHERE oid='porter_identity_config'::regconfig", []string{"namespace", "owned", "cfgparser"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, true, int64(3722)}}))
		for _, tc := range []struct {
			name, args string
			count      int64
		}{
			{"dsnowball_init", "2281", 1}, {"dsnowball_lexize", "2281 2281 2281 2281", 4},
		} {
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT proname,proargtypes::text AS args,pronargs::bigint AS nargs,prorettype::bigint,proisstrict,provolatile,proparallel,prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS c_language FROM pg_proc WHERE proname=$1::name AND pronamespace='pg_catalog'::regnamespace",
				[]string{"proname", "args", "nargs", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "probin", "c_language"},
				[]string{"NAME", "TEXT", "INT8", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "BOOL"},
				[][]any{{tc.name, tc.args, tc.count, int64(2281), true, "v", "u", tc.name, "$libdir/dict_snowball", true}}, tc.name))
		}
		porterAbsentStock(t, p, b, c)
	})
}
