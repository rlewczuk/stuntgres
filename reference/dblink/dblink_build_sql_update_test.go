package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 contrib/dblink/dblink.c and dblink--1.2.sql,
// not from the native implementation or backend comparison.
func TestReferenceDblinkBuildSQLUpdateMetadata(t *testing.T) {
	updateCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT dblink_build_sql_update(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS sql"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function dblink_build_sql_update(text, int2vector, integer, text[], text[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("CREATE EXTENSION dblink SCHEMA dbupd_ext", 0))
		q = "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.pronargdefaults::integer AS defaults,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.proleakproof AS leakproof,p.procost,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='dbupd_ext' AND p.proname='dblink_build_sql_update' AND p.proargtypes::text='25 22 23 1009 1009'"
		updateBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "defaults", "sets", "strict", "volatility", "parallel", "definer", "leakproof", "procost", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "FLOAT4", "TEXT", "TEXT", "TEXT"},
			[][]any{{"dblink_build_sql_update", int64(5), "25 22 23 1009 1009", int64(25), nil, nil, nil, int64(0), false, true, "v", "r", false, false, float64(1), "dblink_build_sql_update", "$libdir/dblink", "c"}}))
		updateBoth(t, p, b, c, updateMember("dblink_build_sql_update", "dbupd_ext"))
		updateBoth(t, p, b, c, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname::text AS schema,e.oid<>0 AND p.oid<>0 AND e.oid<>p.oid AS identities,p.proowner=e.extowner AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid WHERE e.extname='dblink' AND p.proname='dblink_build_sql_update' AND p.proargtypes::text='25 22 23 1009 1009'", []string{"extversion", "extrelocatable", "schema", "identities", "owner"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{"1.2", true, "dbupd_ext", true, true}}))
		held := updateHeld(t, p, c, updateCall)
		for i := range 5 {
			args := []any{"missing_update_relation", "0", int64(0), "{}", "{}"}
			args[i] = nil
			updateBoth(t, p, b, c, updateSelect(updateCall, nil, args...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, updateSelect(updateCall, nil, args...))
		}
	})
}

func TestReferenceDblinkBuildSQLUpdateBinding(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		updateKeys(t, p, b, c)
		const want = "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'"
		for _, q := range []string{
			"SELECT dbupd_ext.dblink_build_sql_update('update_keys','1',1,'{1}','{9}') AS sql",
			"SELECT * FROM dbupd_ext.dblink_build_sql_update('update_keys','1',1,'{1}','{9}') AS t(sql)",
		} {
			updateBoth(t, p, b, c, updateSelect(q, want))
		}
		held := updateHeld(t, p, c, updateCall)
		for _, target := range []string{"9", "10", "9"} {
			s := updateSelect(updateCall, "UPDATE update_keys SET id = '"+target+"', k = 'a', payload = 'O''Brien' WHERE id = '"+target+"'", "update_keys", "1", int64(1), "{1}", "{"+target+"}")
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Preparing a volatile call cannot resolve/open its TEXT relation early.
		const late = "SELECT dbupd_ext.dblink_build_sql_update('update_late','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
		lateHeld := updateHeld(t, p, c, late)
		run(fixture.Exec("CREATE TABLE update_late(id integer)", 0))
		run(fixture.Exec("INSERT INTO update_late VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, lateHeld, b.Recorder, updateSelect(late, "UPDATE update_late SET id = '9' WHERE id = '9'"))
		run(fixture.Exec("CREATE SEQUENCE update_eval START 1", 0))
		// Every operand is demanded once, in written order. The second child
		// yields logical key1 and third yields count1 only in that order.
		q := "SELECT dbupd_ext.dblink_build_sql_update(CASE WHEN nextval('update_eval')=1 THEN 'update_keys' ELSE 'missing' END,(nextval('update_eval')-1)::text::int2vector,(nextval('update_eval')-2)::integer,ARRAY[(nextval('update_eval')-3)::text],ARRAY[(nextval('update_eval')+4)::text]) AS sql"
		ordered := updateHeld(t, p, c, q)
		for _, route := range []string{"direct", "prepared", "held"} {
			run(fixture.Exec("ALTER SEQUENCE update_eval RESTART WITH 1", 0))
			s := updateSelect(q, want)
			s.Prepared = route == "prepared"
			var x fixture.Executor = c
			if route == "held" {
				x = ordered
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			run(fixture.Select("SELECT last_value FROM update_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(5)}}))
		}
	})
}

func TestReferenceDblinkBuildSQLUpdateValues(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		updateKeys(t, p, b, c)
		held := updateHeld(t, p, c, updateCall)
		for _, tc := range []struct {
			vector               string
			count                int64
			source, target, want string
		}{
			{"1", 1, "{01}", "{009}", "UPDATE update_keys SET id = '009', k = 'a', payload = 'O''Brien' WHERE id = '009'"},
			{"2 1", 2, "{a,1}", "{b,9}", "UPDATE update_keys SET id = '9', k = 'b', payload = 'O''Brien' WHERE k = 'b' AND id = '9'"},
			{"1 -1", 1, "{1}", "{9}", "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'"},
			{"1 999", 1, "{1}", "{9}", "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'"},
			{"1 2", 99, "{1,a}", "{9,b}", "UPDATE update_keys SET id = '9', k = 'b', payload = 'O''Brien' WHERE id = '9' AND k = 'b'"},
			{"1 1", 2, "{01,1}", "{9,999}", "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9' AND id = '999'"},
			{"1 2", 2, "{2,NULL}", "{9,NULL}", "UPDATE update_keys SET id = '9', k = NULL, payload = NULL WHERE id = '9' AND k IS NULL"},
			{"1", 1, "{1}", "{bad}", "UPDATE update_keys SET id = 'bad', k = 'a', payload = 'O''Brien' WHERE id = 'bad'"},
			{"1", 1, "{1}", "{NULL}", "UPDATE update_keys SET id = NULL, k = 'a', payload = 'O''Brien' WHERE id IS NULL"},
		} {
			s := updateSelect(updateCall, tc.want, "update_keys", tc.vector, tc.count, tc.source, tc.target)
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

func TestReferenceDblinkBuildSQLUpdateBoundaries(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		updateKeys(t, p, b, c)
		held := updateHeld(t, p, c, updateCall)
		for _, tc := range []struct {
			vector               string
			count                int64
			source, target, want string
		}{
			{"1 2", 2, "[0:1]={1,a}", "[-3:-2]={9,b}", "UPDATE update_keys SET id = '9', k = 'b', payload = 'O''Brien' WHERE id = '9' AND k = 'b'"},
			{"1 2 1 2", 4, "{{1,a},{1,a}}", "{{9,b},{99,c}}", "UPDATE update_keys SET id = '9', k = 'b', payload = 'O''Brien' WHERE id = '9' AND k = 'b' AND id = '99' AND k = 'c'"},
		} {
			s := updateSelect(updateCall, tc.want, "update_keys", tc.vector, tc.count, tc.source, tc.target)
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		bad := updateRead(t, p, b, held, updateSelect(updateCall, "UPDATE update_keys SET id = 'bad', k = 'a', payload = 'O''Brien' WHERE id = 'bad'", "update_keys", "1", int64(1), "{1}", "{bad}"))
		run(fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.LastIndex(bad, "'bad'") + 1)}))
		run(fixture.Select("SELECT id FROM update_keys ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("CREATE TABLE update_text(id text,payload text)", 0))
		run(fixture.Exec("INSERT INTO update_text VALUES(NULL,'null-key'),('NULL','word'),('','empty'),('É雪','unicode'),('O''Brien','quote'),(E'path\\\\tail','slash')", 6))
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
				predicate := "id = " + tc.literal
				if tc.literal == "NULL" {
					predicate = "id IS NULL"
				}
				s := updateSelect(updateCall, "UPDATE update_text SET id = "+tc.literal+", payload = '"+tc.payload+"' WHERE "+predicate, "update_text", "1", int64(1), tc.source, tc.target)
				updateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		run(fixture.Exec("SET standard_conforming_strings=on", 0))
		run(fixture.Exec("INSERT INTO update_text VALUES(NULL,'second-null')", 1))
		s := updateError(updateCall, "21000", "source criteria matched more than one record", "update_text", "1", int64(1), "{NULL}", "{NULL}")
		updateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, q := range []string{"CREATE TABLE update_generated(id integer GENERATED ALWAYS AS IDENTITY,payload integer,g integer GENERATED ALWAYS AS(payload+1) STORED)", "INSERT INTO update_generated(payload) VALUES(4)"} {
			count := int64(0)
			if q[0] == 'I' {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		updateBoth(t, p, b, c, updateSelect(updateCall, "UPDATE update_generated SET id = '9', payload = '4', g = '5' WHERE id = '9'", "update_generated", "1", int64(1), "{1}", "{9}"))
	})
}
