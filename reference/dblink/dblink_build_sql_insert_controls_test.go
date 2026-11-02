package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbins_control_owner", "dbins_control_reader"})
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE dbins_control_owner", "CREATE ROLE dbins_control_reader", "CREATE SCHEMA dbins_moved",
			"GRANT USAGE ON SCHEMA public,dbins_moved TO dbins_control_reader",
			"CREATE TABLE insert_control(id integer,payload text)", "ALTER TABLE insert_control OWNER TO dbins_control_owner",
			"GRANT SELECT ON insert_control TO dbins_control_reader",
			"CREATE VIEW insert_control_saved AS SELECT dbins_ext.dblink_build_sql_insert('insert_control','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql",
			"ALTER FUNCTION dbins_ext.dblink_build_sql_insert" + insertSignature + " RENAME TO insert_builder",
			"ALTER FUNCTION dbins_ext.insert_builder" + insertSignature + " SET SCHEMA dbins_moved",
			"ALTER FUNCTION dbins_moved.insert_builder" + insertSignature + " OWNER TO dbins_control_owner",
			"REVOKE EXECUTE ON FUNCTION dbins_moved.insert_builder" + insertSignature + " FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION dbins_moved.insert_builder" + insertSignature + " TO dbins_control_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO insert_control VALUES(1,'source'),(2,'decoy')", 2))
		const want = "INSERT INTO insert_control(id,payload) VALUES('9','source')"
		saved := insertSelect("SELECT sql FROM insert_control_saved", want)
		q := "SELECT dbins_moved.insert_builder($1::text,$2::int2vector,$3::integer,$4::text[],$5::text[]) AS sql"
		held := insertHeld(t, p, c, q)
		frame := insertSelect(q, want, "insert_control", "1", int64(1), "{1}", "{9}")
		owners := fixture.Select("SELECT r.rolname::text AS table_owner,pr.rolname::text AS routine_owner,has_table_privilege('dbins_control_reader','insert_control','SELECT') AS can_select,has_table_privilege('dbins_control_reader','insert_control','INSERT') AS can_insert,has_function_privilege('dbins_control_reader','dbins_moved.insert_builder(text,int2vector,integer,text[],text[])','EXECUTE') AS can_execute FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner JOIN pg_proc p ON p.proname='insert_builder' JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles pr ON pr.oid=p.proowner WHERE c.relname='insert_control' AND c.relnamespace='public'::regnamespace AND n.nspname='dbins_moved' AND p.proargtypes::text='25 22 23 1009 1009'", []string{"table_owner", "routine_owner", "can_select", "can_insert", "can_execute"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"dbins_control_owner", "dbins_control_owner", true, false, true}})
		rowsCheck := fixture.Select("SELECT id,payload FROM insert_control ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "source"}, {int64(2), "decoy"}})
		dependency := fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_rewrite rw ON rw.oid=d.objid JOIN pg_class v ON v.oid=rw.ev_class JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE d.classid=2618 AND d.refclassid=1255 AND v.relname='insert_control_saved' AND v.relnamespace='public'::regnamespace AND n.nspname='dbins_moved' AND p.proname='insert_builder'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		deleteMember := fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='dblink' AND n.nspname='dbins_ext' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		check := func(x fixture.Executor, r *fixture.Recorder) {
			for _, s := range []fixture.Step{saved, owners, rowsCheck, dependency, insertMember("insert_builder", "dbins_moved"), deleteMember} {
				fixture.RunStep(t, p.Ctx, x, r, s)
				fixture.RunStep(t, p.Ctx, x, r, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(fixture.Exec("SET SESSION AUTHORIZATION dbins_control_reader", 0))
		insertBoth(t, p, b, c, frame)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		if !b.PG {
			run(fixture.Exec("CREATE TEMP TABLE insert_session_keep(id integer)", 0))
			run(fixture.Exec("INSERT INTO insert_session_keep VALUES(73)", 1))
			run(fixture.Exec("SET application_name='insert-control-session'", 0))
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
			run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='dblink'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(c, b.Recorder)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
			run(fixture.Select("SELECT id FROM insert_session_keep ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
			run(fixture.Select("SELECT current_setting('application_name') AS name", []string{"name"}, []string{"TEXT"}, [][]any{{"insert-control-session"}}))
			insertOldText(t, old, columns, types, want)
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
				t.Fatal("logical redump changed after replacement")
			}
			identity := "SELECT p.oid::bigint,e.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid AND d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='dbins_moved' AND p.proname='insert_builder' AND e.extname='dblink'"
			var member, extension int64
			if err := c.QueryRowContext(p.Ctx, identity).Scan(&member, &extension); err != nil {
				t.Fatal(err)
			}
			unchanged := func() {
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
					t.Fatal("rejected replacement changed logical state")
				}
				check(c, b.Recorder)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
				var gotMember, gotExtension int64
				if err := c.QueryRowContext(p.Ctx, identity).Scan(&gotMember, &gotExtension); err != nil || gotMember != member || gotExtension != extension {
					t.Fatal("rejected replacement changed identities", gotMember, gotExtension, err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
				t.Fatal("accepted malformed restore tail")
			}
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled restore", err)
			}
			unchanged()
			other, target, recorder := insertDestination(t, p)
			for _, role := range []string{"dbins_control_owner", "dbins_control_reader"} {
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE ROLE "+role, 0))
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE TABLE insert_destination_collision(id integer)", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE TEMP TABLE insert_destination_temp(id integer)", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("INSERT INTO insert_destination_temp VALUES(82)", 1))
			if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(target, recorder)
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			var targetMember, targetExtension int64
			if err := target.QueryRowContext(p.Ctx, identity).Scan(&targetMember, &targetExtension); err != nil || targetMember <= 0 || targetExtension <= 0 || targetMember == targetExtension {
				t.Fatal("destination-issued identities", targetMember, targetExtension, err)
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT id FROM insert_destination_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(82)}}))
			if fixture.ExtensionDump(t, p.Ctx, other) != dump {
				t.Fatal("independent symbolic replay changed")
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("SET SESSION AUTHORIZATION dbins_control_reader", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("DROP EXTENSION dblink CASCADE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='insert_control_saved' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(rowsCheck)
	})
}

func insertDestination(t *testing.T, p *fixture.Pair) (*sql.DB, *sql.Conn, *fixture.Recorder) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("insert_destination")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	})
	db, recorder := fixture.InsertSelectRecordedPool(t, connector)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return raw, c, recorder
}

func insertOldText(t *testing.T, rows *sql.Rows, columns []string, types []*sql.ColumnType, want string) {
	t.Helper()
	gotColumns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"sql"}) || !reflect.DeepEqual(gotColumns, columns) {
		t.Fatal("retained columns", gotColumns, columns, err)
	}
	if len(types) != 1 || types[0].Name() != "sql" || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal("retained TEXT descriptors", types)
	}
	length, lengthKnown := types[0].Length()
	precision, scale, decimalKnown := types[0].DecimalSize()
	nullable, nullableKnown := types[0].Nullable()
	if length != int64(9223372036854775807) || !lengthKnown || precision != 0 || scale != 0 || decimalKnown || !nullable || !nullableKnown {
		t.Fatal("retained stored-view TEXT metadata", length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown)
	}
	if !rows.Next() {
		t.Fatal("lost old TEXT row", rows.Err())
	}
	var value string
	if err := rows.Scan(&value); err != nil || value != want {
		t.Fatal("old TEXT changed", value, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("old TEXT tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
