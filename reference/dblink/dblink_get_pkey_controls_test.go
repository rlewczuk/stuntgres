package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pkey_control_owner", "pkey_control_reader"})
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE pkey_control_owner", "CREATE ROLE pkey_control_reader", "CREATE SCHEMA pk_right",
			"GRANT USAGE ON SCHEMA public,pk_ext,pk_right TO pkey_control_reader",
			"CREATE TABLE pkey_control(a integer,b integer,PRIMARY KEY(b,a))",
			"ALTER TABLE pkey_control OWNER TO pkey_control_owner",
			"GRANT SELECT ON pkey_control TO pkey_control_reader",
			"CREATE VIEW pkey_control_saved AS SELECT * FROM pk_ext.dblink_get_pkey('pkey_control')",
			"CREATE VIEW pkey_control_value AS SELECT k FROM pk_ext.dblink_get_pkey('pkey_control') AS k",
			"ALTER FUNCTION pk_ext.dblink_get_pkey(text) RENAME TO control_pkey",
			"ALTER FUNCTION pk_ext.control_pkey(text) SET SCHEMA pk_right",
			"ALTER FUNCTION pk_right.control_pkey(text) OWNER TO pkey_control_owner",
			"REVOKE EXECUTE ON FUNCTION pk_right.control_pkey(text) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION pk_right.control_pkey(text) TO pkey_control_reader",
			"ALTER TYPE pk_ext.dblink_pkey_results RENAME TO control_key",
			"ALTER TYPE pk_ext.control_key SET SCHEMA pk_right",
			"ALTER TYPE pk_right.control_key OWNER TO pkey_control_owner",
			"CREATE TABLE pkey_control_values(k pk_right.control_key,ks pk_right.control_key[])",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO pkey_control VALUES(11,22)", 1))
		run(fixture.Exec("INSERT INTO pkey_control_values VALUES(ROW(1,'b')::pk_right.control_key,ARRAY[ROW(2,'a')::pk_right.control_key])", 1))
		const q = "SELECT * FROM pk_right.control_pkey($1::text) ORDER BY position"
		want := [][]any{{int64(1), "b"}, {int64(2), "a"}}
		frame := fixture.Select(q, []string{"position", "colname"}, []string{"INT4", "TEXT"}, want, "pkey_control")
		held := pkeyHeld(t, p, c, q)
		saved := fixture.Select("SELECT * FROM pkey_control_saved ORDER BY position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, want)
		relationships := fixture.Select("SELECT p.prorettype=t.oid AS result,c.reltype=t.oid AS backing,a.typelem=t.oid AS element,t.typowner=r.oid AND a.typowner=r.oid AND c.relowner=r.oid AND p.proowner=r.oid AS owners,has_function_privilege('pkey_control_reader',p.oid,'EXECUTE') AS execute,has_table_privilege('pkey_control_reader','pkey_control','SELECT') AS select FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_type t ON t.typnamespace=n.oid AND t.typname='control_key' JOIN pg_type a ON a.oid=t.typarray JOIN pg_class c ON c.oid=t.typrelid JOIN pg_roles r ON r.rolname='pkey_control_owner' WHERE n.nspname='pk_right' AND p.proname='control_pkey'", []string{"result", "backing", "element", "owners", "execute", "select"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}})
		values := fixture.Select("SELECT k::text AS value,ks::text AS values,pg_typeof(k)::oid='pk_right.control_key'::regtype::oid AS nominal,pg_typeof(ks)::oid=t.typarray AS nominal_array FROM pkey_control_values JOIN pg_type t ON t.oid='pk_right.control_key'::regtype", []string{"value", "values", "nominal", "nominal_array"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"(1,b)", `{"(2,a)"}`, true, true}})
		constructors := fixture.Select("SELECT pk_ext.dblink_build_sql_delete('pkey_control','1'::int2vector,1,'{9}'::text[]) AS del,pk_ext.dblink_build_sql_insert('pkey_control','1'::int2vector,1,'{11}'::text[],'{9}'::text[]) AS ins,pk_ext.dblink_build_sql_update('pkey_control','1'::int2vector,1,'{11}'::text[],'{9}'::text[]) AS upd", []string{"del", "ins", "upd"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"DELETE FROM pkey_control WHERE a = '9'", "INSERT INTO pkey_control(a,b) VALUES('9','22')", "UPDATE pkey_control SET a = '9', b = '22' WHERE a = '9'"}})
		check := func(x fixture.Executor, recorder *fixture.Recorder) {
			for _, s := range []fixture.Step{saved, relationships, values, constructors,
				fixture.Select("SELECT pg_typeof(k)::oid='pk_right.control_key'::regtype::oid AS nominal,k::text AS value FROM pkey_control_value ORDER BY (k).position", []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, "(1,b)"}, {true, "(2,a)"}}),
				fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_rewrite rw ON rw.oid=d.objid JOIN pg_class v ON v.oid=rw.ev_class JOIN pg_proc p ON p.oid=d.refobjid WHERE d.classid=2618 AND d.refclassid=1255 AND v.relname='pkey_control_saved' AND p.proname='control_pkey'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			} {
				fixture.RunStep(t, p.Ctx, x, recorder, s)
				fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_control_reader", 0))
		pkeyRoutes(t, p, b, c, &held, frame)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		if !b.PG {
			run(fixture.Exec("CREATE TEMP TABLE pkey_control_temp(id integer)", 0))
			run(fixture.Exec("INSERT INTO pkey_control_temp VALUES(73)", 1))
			run(fixture.Exec("SET application_name='pkey-control-session'", 0))
			old, err := c.QueryContext(p.Ctx, saved.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			columns, err := old.Columns()
			if err != nil {
				t.Fatal(err)
			}
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(c, b.Recorder)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
			pkeyOldRows(t, old, columns, types, want)
			run(fixture.Select("SELECT id FROM pkey_control_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
			run(fixture.Select("SELECT current_setting('application_name') AS name", []string{"name"}, []string{"TEXT"}, [][]any{{"pkey-control-session"}}))
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
				t.Fatal("logical redump changed")
			}
			const identity = "SELECT p.oid::bigint,t.oid::bigint,t.typarray::bigint,t.typrelid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_type t ON t.oid=p.prorettype WHERE n.nspname='pk_right' AND p.proname='control_pkey'"
			var ids [4]int64
			if err := c.QueryRowContext(p.Ctx, identity).Scan(&ids[0], &ids[1], &ids[2], &ids[3]); err != nil {
				t.Fatal(err)
			}
			unchanged := func() {
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
					t.Fatal("rejected restore changed logical state")
				}
				check(c, b.Recorder)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
				var got [4]int64
				if err := c.QueryRowContext(p.Ctx, identity).Scan(&got[0], &got[1], &got[2], &got[3]); err != nil || got != ids {
					t.Fatal("rejected restore changed installed identities", got, err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
				t.Fatal("malformed restore accepted")
			}
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled restore", err)
			}
			unchanged()
			other, target, recorder := updateDestination(t, p)
			for _, q := range []string{"CREATE ROLE pkey_control_owner", "CREATE ROLE pkey_control_reader", "CREATE TABLE pkey_destination_collision(id integer)", "CREATE TEMP TABLE pkey_destination_temp(id integer)"} {
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("INSERT INTO pkey_destination_temp VALUES(82)", 1))
			if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(target, recorder)
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT id FROM pkey_destination_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(82)}}))
			if fixture.ExtensionDump(t, p.Ctx, other) != dump {
				t.Fatal("independent symbolic replay changed")
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("SET SESSION AUTHORIZATION pkey_control_reader", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			check(c, b.Recorder)
		}
		run(fixture.Exec("DROP EXTENSION dblink CASCADE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname IN ('pkey_control_saved','pkey_control_value') AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT a,b FROM pkey_control ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(22)}}))
	})
}
