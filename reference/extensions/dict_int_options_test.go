package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictIntOptions(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
			held := dictIntHeld(t, p, c)
			defer held.Statement.Close()
			for _, tc := range []struct{ options, stored, input, want string }{
				{"maxlen=6.7", "maxlen = 6.7", "1234567", "{123456}"},
				{"maxlen='  +3tail'", "maxlen = '  +3tail'", "123456", "{123}"},
				{"maxlen='03rest'", "maxlen = '03rest'", "123456", "{123}"},
				{"maxlen=3,rejectlong=1,absval=1", "maxlen = 3, rejectlong = 1, absval = 1", "-123", "{123}"},
				{"maxlen=3,rejectlong=0,absval=0", "maxlen = 3, rejectlong = 0, absval = 0", "-123", "{-12}"},
				{"maxlen=3,rejectlong=ON,absval=OFF", "maxlen = 3, rejectlong = 'on', absval = 'off'", "-123", "{}"},
				{"maxlen=3,rejectlong='OFF',absval='ON'", "maxlen = 3, rejectlong = 'OFF', absval = 'ON'", "-123", "{123}"},
				{"maxlen=2,maxlen=3,rejectlong=true,rejectlong=false,absval=false,absval=true", "maxlen = 2, maxlen = 3, rejectlong = 'true', rejectlong = 'false', absval = 'false', absval = 'true'", "-123456", "{123}"},
			} {
				dictIntExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dict_int_options (TEMPLATE=public.intdict_template,"+tc.options+")", 0)
				dictIntOptionText(t, p, b, c, "dict_int_options", tc.stored)
				for _, e := range []fixture.Executor{c, held} {
					dictIntLexemes(t, p, b, e, tc.want, "dict_int_options", tc.input)
				}
				dictIntExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dict_int_options", 0)
			}
			dictIntExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dict_int_options (TEMPLATE=public.intdict_template,maxlen=3,rejectlong=true,absval=true)", 0)
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (maxlen)", 0)
			dictIntOptionText(t, p, b, c, "dict_int_options", "rejectlong = 'true', absval = 'true'")
			dictIntLexemes(t, p, b, held, "{123456}", "dict_int_options", "-123456")
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (rejectlong)", 0)
			dictIntOptionText(t, p, b, c, "dict_int_options", "absval = 'true'")
			dictIntLexemes(t, p, b, held, "{123456}", "dict_int_options", "-1234567")
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (absval)", 0)
			dictIntOptionText(t, p, b, c, "dict_int_options", nil)
			dictIntLexemes(t, p, b, held, "{-12345}", "dict_int_options", "-1234567")
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (maxlen=1,maxlen=3,rejectlong=true,rejectlong=false,absval=false,absval=true)", 0)
			dictIntOptionText(t, p, b, c, "dict_int_options", "maxlen = 3, rejectlong = 'false', absval = 'true'")
			for _, e := range []fixture.Executor{c, held} {
				dictIntLexemes(t, p, b, e, "{123}", "dict_int_options", "-123456")
			}
		})
	})
	t.Run("invalid", func(t *testing.T) {
		dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE EXTENSION dict_int",
				"CREATE TEXT SEARCH DICTIONARY dict_int_options (TEMPLATE=public.intdict_template,maxlen=3,absval=true)",
				"CREATE TEXT SEARCH CONFIGURATION dict_int_options_cfg (COPY=pg_catalog.simple)",
				"ALTER TEXT SEARCH CONFIGURATION dict_int_options_cfg ALTER MAPPING FOR int,uint WITH dict_int_options",
				"CREATE TABLE dict_int_sentinel(v int)",
			} {
				dictIntExec(t, p, b, c, q, 0)
			}
			dictIntExec(t, p, b, c, "INSERT INTO dict_int_sentinel VALUES(42)", 1)
			held := dictIntHeld(t, p, c)
			defer held.Statement.Close()
			dictIntOptionText(t, p, b, c, "dict_int_options", "maxlen = 3, absval = 'true'")
			const identity = "SELECT oid::bigint,dictname,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid='dict_int_options'::regdictionary::oid"
			const dependencies = "SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=3600 AND objid='dict_int_options'::regdictionary::oid ORDER BY refclassid,refobjid,refobjsubid,deptype"
			const mappings = "SELECT mapcfg::bigint,maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='dict_int_options_cfg'::regconfig::oid ORDER BY maptokentype,mapseqno"
			const dictionaryEdges = "SELECT objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=3600 ORDER BY objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
			allEdges := dictIntRows(t, p, c, dictionaryEdges)
			before, edges, maps := dictIntRows(t, p, c, identity), dictIntRows(t, p, c, dependencies), dictIntRows(t, p, c, mappings)
			check := func() {
				dictIntUnchanged(t, p, c, before, identity)
				dictIntUnchanged(t, p, c, edges, dependencies)
				dictIntUnchanged(t, p, c, maps, mappings)
				dictIntUnchanged(t, p, c, allEdges, dictionaryEdges)
				for _, e := range []fixture.Executor{c, held} {
					dictIntLexemes(t, p, b, e, "{123}", "dict_int_options", "-123456")
				}
				dictIntSelect(t, p, b, c, "SELECT to_tsvector('dict_int_options_cfg','-123456')::text AS vector,to_tsquery('dict_int_options_cfg','-123999')::text AS query", []string{"vector", "query"}, []string{"TEXT", "TEXT"}, [][]any{{"'123':1", "'123'"}})
				dictIntSelect(t, p, b, c, "SELECT v FROM dict_int_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
			}
			check()
			for _, tc := range []struct{ option, code, message string }{
				{"maxlen=0", "22023", "maxlen value has to be >= 1"},
				{"maxlen=-1", "22023", "maxlen value has to be >= 1"},
				{"maxlen='abc'", "22023", "maxlen value has to be >= 1"},
				{"maxlen=''", "22023", "maxlen value has to be >= 1"},
				{"unknown=1", "22023", `unrecognized intdict parameter: "unknown"`},
				{`"MAXLEN"=3`, "22023", `unrecognized intdict parameter: "MAXLEN"`},
				{"rejectlong='yes'", "42601", "rejectlong requires a Boolean value"},
				{"rejectlong='t'", "42601", "rejectlong requires a Boolean value"},
				{"rejectlong=2", "42601", "rejectlong requires a Boolean value"},
				{"rejectlong='1'", "42601", "rejectlong requires a Boolean value"},
				{"absval='no'", "42601", "absval requires a Boolean value"},
				{"absval='f'", "42601", "absval requires a Boolean value"},
				{"absval=2", "42601", "absval requires a Boolean value"},
				{"maxlen=2,unknown=1", "22023", `unrecognized intdict parameter: "unknown"`},
				{"absval=false,rejectlong='yes'", "42601", "rejectlong requires a Boolean value"},
			} {
				for _, prepared := range []bool{false, true} {
					dictIntError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dict_int_bad (TEMPLATE=public.intdict_template,"+tc.option+")", tc.code, tc.message, prepared)
					dictIntSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='dict_int_bad'", []string{"dictname"}, []string{"NAME"}, nil)
					check()
					dictIntError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options ("+tc.option+")", tc.code, tc.message, prepared)
					check()
				}
			}
			for _, option := range []string{"maxlen", "rejectlong", "absval"} {
				for _, prepared := range []bool{false, true} {
					dictIntError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dict_int_bad (TEMPLATE=public.intdict_template,"+option+")", "42601", option+" requires a parameter", prepared)
					dictIntSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='dict_int_bad'", []string{"dictname"}, []string{"NAME"}, nil)
					check()
				}
			}
			dictIntExec(t, p, b, c, "BEGIN", 0)
			dictIntExec(t, p, b, c, "SAVEPOINT dict_int_failed", 0)
			dictIntError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (maxlen=2,unknown=1)", "22023", `unrecognized intdict parameter: "unknown"`, false)
			dictIntExec(t, p, b, c, "ROLLBACK TO SAVEPOINT dict_int_failed", 0)
			check()
			dictIntExec(t, p, b, c, "COMMIT", 0)
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_options (maxlen=2)", 0)
			dictIntLexemes(t, p, b, held, "{12}", "dict_int_options", "-123456")
		})
	})
}
