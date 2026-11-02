package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const deleteCall = "SELECT dbdel_ext.dblink_build_sql_delete($1::text,$2::int2vector,$3::integer,$4::text[]) AS sql"
const deleteSignature = "(text,int2vector,integer,text[])"

func deleteCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"dbdel_ext", "dbdel_left", "dbdel_right", "dbdel_moved", "Del\" Schema"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { if err := c.Close(); err != nil { t.Error(err) } })
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS dblink CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				t.Cleanup(clean)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA dbdel_ext", 0))
			if install {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION dblink SCHEMA dbdel_ext", 0))
			}
			body(t, p, b, c)
		})
	}
}

func deleteSelect(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"sql"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func deleteError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func deleteBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Prepared(s))
}

// Return the observed result, after independent text and metadata assertions, so
// the execution witness cannot accidentally execute only a handwritten golden.
func deleteRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) string {
	t.Helper()
	got := fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	return got.Rows[0][0].(string)
}

func deleteHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { if err := st.Close(); err != nil { t.Error(err) } })
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
}

func deleteMember(qname, schema string) fixture.Step {
	q := "SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND d.objsubid=0 AND d.refobjsubid=0 AND e.extname='dblink' AND n.nspname='" + schema + "' AND p.proname='" + qname + "' AND p.proargtypes::text='25 22 23 1009'"
	return fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
}

func TestReferenceDblinkBuildSQLDeleteMetadata(t *testing.T) {
	deleteCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT dblink_build_sql_delete(NULL::text,NULL::int2vector,NULL::integer,NULL::text[]) AS sql"
		d := fixture.Diagnostic{Code: "42883", Message: "function dblink_build_sql_delete(text, int2vector, integer, text[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
		s := fixture.ImplicitError(q, d)
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("CREATE EXTENSION dblink SCHEMA dbdel_ext", 0))
		q = "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.pronargdefaults::integer AS defaults,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.proleakproof AS leakproof,p.procost,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='dbdel_ext' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'"
		deleteBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "defaults", "sets", "strict", "volatility", "parallel", "definer", "leakproof", "procost", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "FLOAT4", "TEXT", "TEXT", "TEXT"},
			[][]any{{"dblink_build_sql_delete", int64(4), "25 22 23 1009", int64(25), nil, nil, nil, int64(0), false, true, "v", "r", false, false, float64(1), "dblink_build_sql_delete", "$libdir/dblink", "c"}}))
		deleteBoth(t, p, b, c, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname::text AS schema,e.oid<>0 AND p.oid<>0 AND n.oid<>0 AND e.oid<>p.oid AND e.oid<>n.oid AND p.oid<>n.oid AS identities,p.proowner=e.extowner AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid WHERE e.extname='dblink' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'",
			[]string{"extversion", "extrelocatable", "schema", "identities", "owner"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{"1.2", true, "dbdel_ext", true, true}}))
		deleteBoth(t, p, b, c, deleteMember("dblink_build_sql_delete", "dbdel_ext"))
		deleteBoth(t, p, b, c, fixture.Select("SELECT superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='dblink' AND version='1.2'", []string{"superuser", "trusted", "relocatable"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true}}))
		run(fixture.Exec("CREATE TABLE delete_keys(id text)", 0))
		for i := range 4 {
			args := []any{"missing_delete_relation", "1", int64(1), "{a}"}
			args[i] = nil
			deleteBoth(t, p, b, c, deleteSelect(deleteCall, nil, args...))
		}
		deleteBoth(t, p, b, c, deleteSelect(deleteCall, "DELETE FROM delete_keys WHERE id IS NULL", "delete_keys", "1", int64(1), "{NULL}"))
	})
}

func TestReferenceDblinkBuildSQLDeleteBehavior(t *testing.T) {
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE "D""T"("K""id" text,k2 text,payload text)`, 0))
		const want = `DELETE FROM "D""T" WHERE "K""id" = 'O''Brien' AND k2 IS NULL`
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec(`TRUNCATE "D""T"`, 0))
			s := deleteSelect(deleteCall, want, `"D""T"`, "1 2", int64(2), `{"O'Brien",NULL}`)
			s.Prepared = prepared
			generated := deleteRead(t, p, b, c, s) // empty local table is sufficient
			run(fixture.Exec(`INSERT INTO "D""T" VALUES ('O''Brien',NULL,'target'),('other',NULL,'decoy1'),('O''Brien','not-null','decoy2')`, 3))
			deleteRead(t, p, b, c, s)
			run(fixture.Select(`SELECT payload FROM "D""T" ORDER BY payload`, []string{"payload"}, []string{"TEXT"}, [][]any{{"decoy1"}, {"decoy2"}, {"target"}}))
			run(fixture.Exec(generated, 1))
			run(fixture.Select(`SELECT "K""id",k2,payload FROM "D""T" ORDER BY payload`, []string{"K\"id", "k2", "payload"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"other", nil, "decoy1"}, {"O'Brien", "not-null", "decoy2"}}))
			s = deleteSelect(deleteCall, `DELETE FROM "D""T" WHERE "K""id" = 'absent'`, `"D""T"`, "1", int64(1), "{absent}")
			s.Prepared = prepared
			run(fixture.Exec(deleteRead(t, p, b, c, s), 0))
			run(fixture.Exec(`INSERT INTO "D""T" VALUES ('O''Brien',NULL,'duplicate1'),('O''Brien',NULL,'duplicate2')`, 2))
			s = deleteSelect(deleteCall, want, `"D""T"`, "1 2", int64(2), `{"O'Brien",NULL}`)
			s.Prepared = prepared
			run(fixture.Exec(deleteRead(t, p, b, c, s), 2))
			run(fixture.Select(`SELECT payload FROM "D""T" ORDER BY payload`, []string{"payload"}, []string{"TEXT"}, [][]any{{"decoy1"}, {"decoy2"}}))
		}
		run(fixture.Exec("CREATE TABLE delete_integer(id integer)", 0))
		for _, prepared := range []bool{false, true} {
			s := deleteSelect(deleteCall, "DELETE FROM delete_integer WHERE id = 'not-an-int'", "delete_integer", "1", int64(1), "{not-an-int}")
			s.Prepared = prepared
			generated := deleteRead(t, p, b, c, s)
			d := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "not-an-int"`, Position: fixture.Position(strings.Index(generated, "'not-an-int'") + 1)}
			run(fixture.ImplicitError(generated, d))
		}
	})
}

func TestReferenceDblinkBuildSQLDeleteQuoting(t *testing.T) {
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE delete_keys(id text,payload text)", 0))
		run(fixture.Exec("CREATE TABLE sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		held := deleteHeld(t, p, c, deleteCall)
		for _, setting := range []string{"on", "off"} {
			run(fixture.Exec("SET standard_conforming_strings="+setting, 0))
			for _, tc := range []struct{ value any; array, literal string }{
				{"", `{""}`, "''"}, {"NULL", `{"NULL"}`, "'NULL'"}, {nil, `{NULL}`, "IS NULL"},
				{"O'Brien", `{"O'Brien"}`, "'O''Brien'"}, {`a\b'c`, `{"a\\b'c"}`, `E'a\\b''c'`},
				{"É雪", `{É雪}`, "'É雪'"}, {"x'; DELETE FROM sentinel; --", `{"x'; DELETE FROM sentinel; --"}`, "'x''; DELETE FROM sentinel; --'"},
			} {
				for _, route := range []string{"direct", "prepared", "held"} {
					run(fixture.Exec("TRUNCATE delete_keys", 0))
					run(fixture.Exec("INSERT INTO delete_keys VALUES($1,'target'),('unrelated','decoy')", 2, tc.value))
					predicate := "id = " + tc.literal
					if tc.value == nil { predicate = "id IS NULL" }
					s := deleteSelect(deleteCall, "DELETE FROM delete_keys WHERE "+predicate, "delete_keys", "1", int64(1), tc.array)
					s.Prepared = route == "prepared"
					var x fixture.Executor = c
					if route == "held" { x = held }
					generated := deleteRead(t, p, b, x, s)
					run(fixture.Exec(generated, 1))
					run(fixture.Select("SELECT id,payload FROM delete_keys ORDER BY payload", []string{"id", "payload"}, []string{"TEXT", "TEXT"}, [][]any{{"unrelated", "decoy"}}))
					run(fixture.Select("SELECT id FROM sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
				}
			}
		}
		run(fixture.Exec("SET standard_conforming_strings=on", 0))
		const literalCall = "SELECT dbdel_ext.dblink_build_sql_delete('delete_keys','1'::int2vector,1,'{a}'::text[]) AS sql"
		literalHeld := deleteHeld(t, p, c, literalCall)
		for _, setting := range []string{"on", "off", "on"} {
			run(fixture.Exec("SET quote_all_identifiers="+setting, 0))
			want := "DELETE FROM delete_keys WHERE id = 'a'"
			if setting == "on" { want = `DELETE FROM "delete_keys" WHERE "id" = 'a'` }
			s := deleteSelect(deleteCall, want, "delete_keys", "1", int64(1), "{a}")
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, literalHeld, b.Recorder, deleteSelect(literalCall, want))
		}
		run(fixture.Exec("SET quote_all_identifiers=off", 0))
		run(fixture.Exec(`CREATE SCHEMA "Del"" Schema"`, 0))
		run(fixture.Exec(`CREATE TABLE "Del"" Schema"."Mixed Table"("K""ey" text,"between" text,"select" text)`, 0))
		const want = `DELETE FROM "Del"" Schema"."Mixed Table" WHERE "K""ey" = 'a' AND "between" = 'b' AND "select" = 'c'`
		s := deleteSelect(deleteCall, want, `"Del"" Schema"."Mixed Table"`, "1 2 3", int64(3), "{a,b,c}")
		deleteBoth(t, p, b, c, s)
		run(fixture.Exec(`INSERT INTO "Del"" Schema"."Mixed Table" VALUES('a','b','c'),('a','b','other')`, 2))
		run(fixture.Exec(deleteRead(t, p, b, held, s), 1))
		run(fixture.Select(`SELECT "select" FROM "Del"" Schema"."Mixed Table" ORDER BY "select"`, []string{"select"}, []string{"TEXT"}, [][]any{{"other"}}))
	})
}
