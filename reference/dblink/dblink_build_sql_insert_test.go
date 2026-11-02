package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 contrib/dblink/dblink.c and dblink--1.2.sql,
// not from the native implementation or backend comparison.
func TestReferenceDblinkBuildSQLInsertMetadata(t *testing.T) {
	insertCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT dblink_build_sql_insert(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS sql"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function dblink_build_sql_insert(text, int2vector, integer, text[], text[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("CREATE EXTENSION dblink SCHEMA dbins_ext", 0))
		q = "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.pronargdefaults::integer AS defaults,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.proleakproof AS leakproof,p.procost,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='dbins_ext' AND p.proname='dblink_build_sql_insert' AND p.proargtypes::text='25 22 23 1009 1009'"
		insertBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "defaults", "sets", "strict", "volatility", "parallel", "definer", "leakproof", "procost", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "FLOAT4", "TEXT", "TEXT", "TEXT"},
			[][]any{{"dblink_build_sql_insert", int64(5), "25 22 23 1009 1009", int64(25), nil, nil, nil, int64(0), false, true, "v", "r", false, false, float64(1), "dblink_build_sql_insert", "$libdir/dblink", "c"}}))
		insertBoth(t, p, b, c, insertMember("dblink_build_sql_insert", "dbins_ext"))
		insertBoth(t, p, b, c, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname::text AS schema,e.oid<>0 AND p.oid<>0 AND e.oid<>p.oid AS identities,p.proowner=e.extowner AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid WHERE e.extname='dblink' AND p.proname='dblink_build_sql_insert' AND p.proargtypes::text='25 22 23 1009 1009'", []string{"extversion", "extrelocatable", "schema", "identities", "owner"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{"1.2", true, "dbins_ext", true, true}}))
		held := insertHeld(t, p, c, insertCall)
		for i := range 5 {
			args := []any{"missing_insert_relation", "0", int64(0), "{}", "{}"}
			args[i] = nil
			insertBoth(t, p, b, c, insertSelect(insertCall, nil, args...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, insertSelect(insertCall, nil, args...))
		}
	})
}

func TestReferenceDblinkBuildSQLInsertBinding(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		insertKeys(t, p, b, c)
		const want = "INSERT INTO insert_keys(id,k,payload) VALUES('9','a','O''Brien')"
		for _, q := range []string{
			"SELECT dbins_ext.dblink_build_sql_insert('insert_keys','1',1,'{1}','{9}') AS sql",
			"SELECT * FROM dbins_ext.dblink_build_sql_insert('insert_keys','1',1,'{1}','{9}') AS t(sql)",
		} {
			insertBoth(t, p, b, c, insertSelect(q, want))
		}
		held := insertHeld(t, p, c, insertCall)
		for _, target := range []string{"9", "10", "9"} {
			s := insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('"+target+"','a','O''Brien')", "insert_keys", "1", int64(1), "{1}", "{"+target+"}")
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Preparing a volatile call cannot resolve/open its TEXT relation early.
		const late = "SELECT dbins_ext.dblink_build_sql_insert('insert_late','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
		lateHeld := insertHeld(t, p, c, late)
		run(fixture.Exec("CREATE TABLE insert_late(id integer)", 0))
		run(fixture.Exec("INSERT INTO insert_late VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, lateHeld, b.Recorder, insertSelect(late, "INSERT INTO insert_late(id) VALUES('9')"))
		run(fixture.Exec("CREATE SEQUENCE insert_eval START 1", 0))
		// Every operand is demanded once, in written order. The second child
		// yields logical key1 and third yields count1 only in that order.
		q := "SELECT dbins_ext.dblink_build_sql_insert(CASE WHEN nextval('insert_eval')=1 THEN 'insert_keys' ELSE 'missing' END,(nextval('insert_eval')-1)::text::int2vector,(nextval('insert_eval')-2)::integer,ARRAY[(nextval('insert_eval')-3)::text],ARRAY[(nextval('insert_eval')+4)::text]) AS sql"
		ordered := insertHeld(t, p, c, q)
		for _, route := range []string{"direct", "prepared", "held"} {
			run(fixture.Exec("ALTER SEQUENCE insert_eval RESTART WITH 1", 0))
			s := insertSelect(q, want)
			s.Prepared = route == "prepared"
			var x fixture.Executor = c
			if route == "held" {
				x = ordered
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			run(fixture.Select("SELECT last_value FROM insert_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(5)}}))
		}
	})
}

func TestReferenceDblinkBuildSQLInsertValues(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		insertKeys(t, p, b, c)
		held := insertHeld(t, p, c, insertCall)
		for _, tc := range []struct {
			vector               string
			count                int64
			source, target, want string
		}{
			{"1", 1, "{01}", "{009}", "INSERT INTO insert_keys(id,k,payload) VALUES('009','a','O''Brien')"},
			{"2 1", 2, "{a,1}", "{b,9}", "INSERT INTO insert_keys(id,k,payload) VALUES('9','b','O''Brien')"},
			{"1 999", 1, "{1}", "{9}", "INSERT INTO insert_keys(id,k,payload) VALUES('9','a','O''Brien')"},
			{"1 2", 99, "{1,a}", "{9,b}", "INSERT INTO insert_keys(id,k,payload) VALUES('9','b','O''Brien')"},
			{"1 1", 2, "{01,1}", "{9,999}", "INSERT INTO insert_keys(id,k,payload) VALUES('9','a','O''Brien')"},
			{"1 2", 2, "{2,NULL}", "{9,NULL}", "INSERT INTO insert_keys(id,k,payload) VALUES('9',NULL,NULL)"},
			{"1", 1, "{1}", "{bad}", "INSERT INTO insert_keys(id,k,payload) VALUES('bad','a','O''Brien')"},
			{"1", 1, "{1}", "{NULL}", "INSERT INTO insert_keys(id,k,payload) VALUES(NULL,'a','O''Brien')"},
		} {
			s := insertSelect(insertCall, tc.want, "insert_keys", tc.vector, tc.count, tc.source, tc.target)
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

func TestReferenceDblinkBuildSQLInsertBoundaries(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		insertKeys(t, p, b, c)
		held := insertHeld(t, p, c, insertCall)
		for _, tc := range []struct {
			vector         string
			count          int64
			source, target string
		}{
			{"1 2", 2, "[0:1]={1,a}", "[-3:-2]={9,b}"},
			{"1 2 1 2", 4, "{{1,a},{1,a}}", "{{9,b},{99,c}}"},
		} {
			s := insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('9','b','O''Brien')", "insert_keys", tc.vector, tc.count, tc.source, tc.target)
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		bad := insertRead(t, p, b, held, insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('bad','a','O''Brien')", "insert_keys", "1", int64(1), "{1}", "{bad}"))
		run(fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.Index(bad, "'bad'") + 1)}))
		run(fixture.Select("SELECT id FROM insert_keys ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("CREATE TABLE insert_text(id text,payload text)", 0))
		run(fixture.Exec("INSERT INTO insert_text VALUES(NULL,'null-key'),('NULL','word'),('','empty'),('É雪','unicode'),('O''Brien','quote'),(E'path\\\\tail','slash')", 6))
		for _, setting := range []string{"on", "off"} {
			run(fixture.Exec("SET standard_conforming_strings="+setting, 0))
			for _, tc := range []struct{ source, target, payload, literal string }{
				{"{NULL}", "{NULL}", "null-key", "NULL"},
				{`{"NULL"}`, `{"NULL"}`, "word", "'NULL'"},
				{`{""}`, `{""}`, "empty", "''"},
				{"{É雪}", "{É雪}", "unicode", "'É雪'"},
				{`{"O'Brien"}`, `{"remote's"}`, "quote", "'remote''s'"},
				{`{"path\\tail"}`, `{"new\\tail"}`, "slash", `E'new\\tail'`},
			} {
				s := insertSelect(insertCall, "INSERT INTO insert_text(id,payload) VALUES("+tc.literal+",'"+tc.payload+"')", "insert_text", "1", int64(1), tc.source, tc.target)
				insertBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		run(fixture.Exec("SET standard_conforming_strings=on", 0))
		for _, q := range []string{"CREATE TABLE insert_generated(id integer GENERATED ALWAYS AS IDENTITY,payload integer,g integer GENERATED ALWAYS AS(payload+1) STORED)", "INSERT INTO insert_generated(payload) VALUES(4)"} {
			count := int64(0)
			if q[0] == 'I' {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		insertBoth(t, p, b, c, insertSelect(insertCall, "INSERT INTO insert_generated(id,payload,g) VALUES('9','4','5')", "insert_generated", "1", int64(1), "{1}", "{9}"))
	})
}
