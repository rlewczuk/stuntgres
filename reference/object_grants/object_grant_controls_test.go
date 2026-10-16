package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantControls(t *testing.T) {
	t.Run("dump_preflight", func(t *testing.T) {
		objectGrantScenario(t, []string{"og_control_members"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			builtins := []string{
				"pg_catalog.abs(integer)", "pg_catalog.sum(integer)",
				"pg_catalog.row_number()", "pg_catalog.int4pl(integer,integer)",
				"pg_catalog.generate_series(integer,integer)",
				"pg_catalog.convert_to(text,name)",
			}
			defer objectGrantRoutineCleanup(t, p, c, builtins)
			defer func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP OWNED BY og_object_reader CASCADE", "DROP OWNED BY og_object_other CASCADE", "GRANT ALL ON PARAMETER lc_messages TO CURRENT_USER"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error("control ACL cleanup", q, err)
					}
				}
			}()
			reject := func(message string) {
				if b.PG {
					return // SQL witnesses below are the ordinary PostgreSQL counterpart.
				}
				var writer objectGrantWriteProbe
				err := stuntgres.Dump(p.Ctx, p.Native, &writer)
				if err == nil || err.Error() != message || writer.calls != 0 {
					t.Fatal("Dump must preflight before publishing even a prefix", err, writer.calls, message)
				}
			}
			baseline := func() {
				if !b.PG {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					var out bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
						t.Fatal("initialized baseline must remain dumpable", err)
					}
				}
			}
			for _, tc := range []struct {
				target, query, typ string
				rows               [][]any
			}{
				{builtins[0], "SELECT abs(-3::integer) AS v", "INT4", [][]any{{int64(3)}}},
				{builtins[1], "SELECT sum(x) AS v FROM (VALUES(1),(2)) s(x)", "INT8", [][]any{{int64(3)}}},
				{builtins[2], "SELECT row_number() OVER() AS v", "INT8", [][]any{{int64(1)}}},
				{builtins[3], "SELECT 1+2 AS v", "INT4", [][]any{{int64(3)}}},
				{builtins[4], "SELECT generate_series(1,2) AS v ORDER BY v", "INT4", [][]any{{int64(1)}, {int64(2)}}},
				{builtins[5], "SELECT convert_to('A','UTF8') AS v", "BYTEA", [][]any{{"A"}}},
			} {
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, tc.rows)) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				name := strings.TrimPrefix(strings.Split(tc.target, "(")[0], "pg_catalog.")
				kind := "function"
				if name == "sum" {
					kind = "aggregate"
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, tc.query, kind, name) })
				message := "stuntgres: dump cannot serialize altered builtin routine authority"
				// convert_to is an ordinary builtin; conversion member routines are
				// covered independently below, not conflated with SQL conversion.
				reject(message)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO PUBLIC", 0))
				baseline()
			}
			for _, ext := range []struct {
				name, target, query, typ string
				rows                     [][]any
			}{
				{"unaccent", "unaccent(text)", "SELECT og_control_members.unaccent('Hôtel') AS v", "TEXT", [][]any{{"Hotel"}}},
				{"pgcrypto", "digest(text,text)", "SELECT encode(og_control_members.digest('abc','md5'),'hex') AS v", "TEXT", [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}},
				{"fuzzystrmatch", "daitch_mokotoff(text)", "SELECT og_control_members.daitch_mokotoff('George')::text AS v", "TEXT", [][]any{{"{595000}"}}},
			} {
				run(fixture.Exec("CREATE SCHEMA og_control_members AUTHORIZATION og_object_reader", 0))
				run(fixture.Exec("CREATE EXTENSION "+ext.name+" SCHEMA og_control_members", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(ext.query, []string{"v"}, []string{ext.typ}, ext.rows)) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION og_control_members."+ext.target+" FROM PUBLIC", 0))
				reject("stuntgres: dump cannot serialize explicit routine ACL")
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION og_control_members."+ext.target+" TO PUBLIC", 0))
				if b.PG {
					run(fixture.Exec("DROP EXTENSION "+ext.name, 0))
					run(fixture.Exec("DROP SCHEMA og_control_members", 0))
				}
				baseline()
			}
			codec := "pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)"
			defer objectGrantRoutineCleanup(t, p, c, []string{codec})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+codec+" FROM PUBLIC", 0))
			reject("stuntgres: dump cannot serialize altered builtin conversion routine authority")
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+codec+" TO PUBLIC", 0))
			baseline()
			// A default-equivalent grant/revoke removes the sparse parameter entry;
			// explicitly empty authority is deliberately not the same baseline.
			run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("SET lc_messages='C'", 0)) })
			reject("stuntgres: dump cannot serialize parameter ACL")
			run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_reader", 0))
			if !b.PG {
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
					t.Fatal("default-equivalent parameter ACL must be removed", err)
				}
			}
			run(fixture.Exec("REVOKE ALL ON PARAMETER lc_messages FROM CURRENT_USER", 0))
			reject("stuntgres: dump cannot serialize parameter ACL")
			baseline()
			run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO og_object_reader", 0))
			reject("stuntgres: dump cannot serialize altered tablespace authority")
			run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM og_object_reader", 0))
			// Even explicit default-equivalent tablespace ACLs cannot be silently
			// discarded, unlike sparse default-equivalent parameter entries.
			reject("stuntgres: dump cannot serialize altered tablespace authority")
			baseline()
		})
	})

	t.Run("restore_atomicity_and_replacement", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("CREATE TABLE og_control_data(x INT)", 0))
			run(fixture.Exec("INSERT INTO og_control_data VALUES(7)", 1))
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE", 0))
			_, database := fixture.RoleGrantEnvironment(t, p, c)
			run(fixture.Exec("GRANT CREATE ON DATABASE "+pq.QuoteIdentifier(database)+" TO og_object_other", 0))
			if b.PG {
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE "+pq.QuoteIdentifier(database)+" FROM og_object_other"); err != nil {
						t.Error("destination policy cleanup", err)
					}
				}()
			}
			run(fixture.Exec("RESET application_name", 0))
			read := fixture.Select("SELECT x FROM og_control_data", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
			// These genuine ordinary SQL counterparts precede native control phases.
			run(read)
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("SELECT abs(-3::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
				fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
			})
			if b.PG {
				return
			}
			// Release the scenario's native SQL borrower before replacement APIs.
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			run = func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			run(fixture.Exec("CREATE SEQUENCE og_control_sequence", 0))
			var baseline bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal(err)
			}
			var databaseACL string
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT datacl::TEXT FROM pg_database WHERE datname=current_database()").Scan(&databaseACL); err != nil {
				t.Fatal(err)
			}
			for _, grant := range []string{
				"GRANT USAGE ON SEQUENCE og_control_sequence TO og_object_reader",
				"GRANT ALL ON DATABASE " + pq.QuoteIdentifier(database) + " TO og_object_reader WITH GRANT OPTION",
				"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader",
				"GRANT SET ON PARAMETER lc_messages TO og_object_reader",
				"GRANT CREATE ON TABLESPACE pg_default TO og_object_reader",
			} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE og_control_prefix(x INT); SET application_name='unpublished'; "+grant+";"))
				fixture.State(t, err, "0A000")
				run(read)
				run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='og_control_prefix'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
				var unchanged bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(baseline.Bytes(), unchanged.Bytes()) {
					t.Fatal("failed Restore published data or authority", err)
				}
				var afterACL string
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT datacl::TEXT FROM pg_database WHERE datname=current_database()").Scan(&afterACL); err != nil || afterACL != databaseACL {
					t.Fatal("failed Restore changed destination attachment authority", err, afterACL, databaseACL)
				}
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, read.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			oldRows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oldRows.Close()
			for _, q := range []string{
				"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC",
				"GRANT SET ON PARAMETER lc_messages TO og_object_reader",
				"GRANT CREATE ON TABLESPACE pg_default TO og_object_reader",
			} {
				run(fixture.Exec(q, 0))
			}
			// Only the deliberately retained Rows/held statement survive replacement.
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceConsume(t, b.PG, oldRows, nil, read)
			run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='og_control_data'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes())); err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: read.Query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, read)
			run(read)
			// Independent post-replacement baseline families and principal policy.
			run(fixture.Select("SELECT spcacl IS NULL AS baseline FROM pg_tablespace WHERE spcname='pg_default'", []string{"baseline"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Select("SELECT r.rolname FROM pg_roles r WHERE r.rolname IN ('og_object_reader','og_object_other') ORDER BY r.rolname", []string{"rolname"}, []string{"NAME"}, [][]any{{"og_object_other"}, {"og_object_reader"}}))
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			frun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, fresh, b.Recorder, s) }
			fixture.RoleGrantActor(frun, "og_object_reader", func() {
				frun(fixture.Select("SELECT abs(-3::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
				fixture.RoleGrantError(frun, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
				// Retained membership supplies destination database CREATE after import.
				frun(fixture.Exec("CREATE SCHEMA og_control_policy", 0))
				frun(fixture.Exec("DROP SCHEMA og_control_policy", 0))
			})
		})
	})
}
