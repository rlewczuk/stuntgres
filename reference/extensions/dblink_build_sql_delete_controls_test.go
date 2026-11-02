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

func TestReferenceDblinkBuildSQLDeleteControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbdel_control_owner", "dbdel_control_reader"})
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE dbdel_control_owner", "CREATE ROLE dbdel_control_reader", "CREATE SCHEMA dbdel_moved",
			"GRANT USAGE ON SCHEMA public,dbdel_moved TO dbdel_control_reader",
			"CREATE TABLE delete_control(id text,payload text)", "ALTER TABLE delete_control OWNER TO dbdel_control_owner",
			"GRANT SELECT ON delete_control TO dbdel_control_reader",
			"CREATE VIEW delete_control_saved AS SELECT dbdel_ext.dblink_build_sql_delete('delete_control','1'::int2vector,1,'{a}'::text[]) AS sql",
			"ALTER FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" RENAME TO delete_builder",
			"ALTER FUNCTION dbdel_ext.delete_builder"+deleteSignature+" SET SCHEMA dbdel_moved",
			"ALTER FUNCTION dbdel_moved.delete_builder"+deleteSignature+" OWNER TO dbdel_control_owner",
			"REVOKE EXECUTE ON FUNCTION dbdel_moved.delete_builder"+deleteSignature+" FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION dbdel_moved.delete_builder"+deleteSignature+" TO dbdel_control_reader",
		} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO delete_control VALUES('a','target'),('b','decoy')", 2))
		const want = "DELETE FROM delete_control WHERE id = 'a'"
		saved := deleteSelect("SELECT sql FROM delete_control_saved", want)
		q := "SELECT dbdel_moved.delete_builder($1::text,$2::int2vector,$3::integer,$4::text[]) AS sql"
		held := deleteHeld(t, p, c, q)
		frame := deleteSelect(q, want, "delete_control", "1", int64(1), "{a}")
		owners := fixture.Select("SELECT r.rolname::text AS table_owner,pr.rolname::text AS routine_owner,has_table_privilege('dbdel_control_reader','delete_control','SELECT') AS can_select,has_table_privilege('dbdel_control_reader','delete_control','DELETE') AS can_delete,has_function_privilege('dbdel_control_reader','dbdel_moved.delete_builder(text,int2vector,integer,text[])','EXECUTE') AS can_execute FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner JOIN pg_proc p ON p.proname='delete_builder' JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles pr ON pr.oid=p.proowner WHERE c.relname='delete_control' AND c.relnamespace='public'::regnamespace AND n.nspname='dbdel_moved' AND p.proargtypes::text='25 22 23 1009'",
			[]string{"table_owner", "routine_owner", "can_select", "can_delete", "can_execute"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"dbdel_control_owner", "dbdel_control_owner", true, false, true}})
		rowsCheck := fixture.Select("SELECT id,payload FROM delete_control ORDER BY id", []string{"id", "payload"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "target"}, {"b", "decoy"}})
		dependency := fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_rewrite rw ON rw.oid=d.objid JOIN pg_class v ON v.oid=rw.ev_class JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE d.classid=2618 AND d.refclassid=1255 AND v.relname='delete_control_saved' AND v.relnamespace='public'::regnamespace AND n.nspname='dbdel_moved' AND p.proname='delete_builder'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		check := func(x fixture.Executor, recorder *fixture.Recorder) {
			for _, s := range []fixture.Step{saved, owners, rowsCheck, dependency, deleteMember("delete_builder", "dbdel_moved")} {
				fixture.RunStep(t, p.Ctx, x, recorder, s)
				fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_control_reader", 0))
		deleteBoth(t, p, b, c, frame)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		if !b.PG {
			// Native-only replacement APIs; the SQL assertions above and below
			// remain the same shared public scenario, not an alternate test body.
			run(fixture.Exec("CREATE TEMP TABLE delete_session_keep(id integer)", 0))
			run(fixture.Exec("INSERT INTO delete_session_keep VALUES(73)", 1))
			run(fixture.Exec("SET application_name='delete-control-session'", 0))
			old, err := c.QueryContext(p.Ctx, saved.Query)
			if err != nil { t.Fatal(err) }
			defer old.Close()
			columns, err := old.Columns()
			if err != nil { t.Fatal(err) }
			types, err := old.ColumnTypes()
			if err != nil { t.Fatal(err) }
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='dblink'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbdel_moved' AND p.proname='delete_builder'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			check(c, b.Recorder)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
			run(fixture.Select("SELECT id FROM delete_session_keep ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
			run(fixture.Select("SELECT current_setting('application_name') AS name", []string{"name"}, []string{"TEXT"}, [][]any{{"delete-control-session"}}))
			deleteOldText(t, old, columns, types, want)
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump { t.Fatal("logical redump changed after replacement") }
			// Capture destination identity, rather than requiring imported source
			// OIDs to authorize restored execution or requiring numeric inequality.
			var member, extension int64
			identity := "SELECT p.oid::bigint,e.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid AND d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='dbdel_moved' AND p.proname='delete_builder' AND e.extname='dblink'"
			if err := c.QueryRowContext(p.Ctx, identity).Scan(&member, &extension); err != nil { t.Fatal(err) }
			unchanged := func() {
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump { t.Fatal("rejected replacement changed logical state") }
				check(c, b.Recorder)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
				var gotMember, gotExtension int64
				if err := c.QueryRowContext(p.Ctx, identity).Scan(&gotMember, &gotExtension); err != nil || gotMember != member || gotExtension != extension { t.Fatal("rejected replacement changed identities", gotMember, gotExtension, err) }
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil { t.Fatal("accepted malformed restore tail") }
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) { t.Fatal("canceled restore", err) }
			unchanged()
			other, target, recorder := pgcryptoControlDestination(t, p, "delete_destination")
			for _, role := range []string{"dbdel_control_owner", "dbdel_control_reader"} {
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE ROLE "+role, 0))
			}
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE TABLE delete_destination_collision(id integer)", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE TEMP TABLE delete_destination_temp(id integer)", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("INSERT INTO delete_destination_temp VALUES(82)", 1))
			if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			check(target, recorder)
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT id FROM delete_destination_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(82)}}))
			if fixture.ExtensionDump(t, p.Ctx, other) != dump { t.Fatal("independent symbolic replay changed") }
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("SET SESSION AUTHORIZATION dbdel_control_reader", 0))
			fixture.RunStep(t, p.Ctx, target, recorder, frame)
			fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// Ordinary dependency closure is required in both backends.
		run(fixture.Exec("DROP EXTENSION dblink CASCADE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='delete_control_saved' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(rowsCheck)
	})
}

func deleteOldText(t *testing.T, rows *sql.Rows, columns []string, types []*sql.ColumnType, want string) {
	t.Helper()
	gotColumns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"sql"}) || !reflect.DeepEqual(gotColumns, columns) { t.Fatal("retained columns", gotColumns, columns, err) }
	if len(types) != 1 || types[0].Name() != "sql" || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() { t.Fatal("retained TEXT descriptors", types) }
	length, lengthKnown := types[0].Length()
	precision, scale, decimalKnown := types[0].DecimalSize()
	nullable, nullableKnown := types[0].Nullable()
	// These rows select a stored view column, not a direct computed target.
	// Native view declarations expose known nullable metadata (the same
	// contract as fixture.FormatReferenceMetadata with storedView=true).
	if length != int64(9223372036854775807) || !lengthKnown || precision != 0 || scale != 0 || decimalKnown || !nullable || !nullableKnown {
		t.Fatal("retained stored-view TEXT metadata", length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown)
	}
	if !rows.Next() { t.Fatal("lost old TEXT row", rows.Err()) }
	var value string
	if err := rows.Scan(&value); err != nil || value != want { t.Fatal("old TEXT changed", value, err) }
	if rows.Next() || rows.Err() != nil { t.Fatal("old TEXT tail", rows.Err()) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
}

func TestReferenceDblinkBuildSQLDeleteFeatureSmoke(t *testing.T) {
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE "Smoke Table"("K""id" text,k2 text,payload text)`, 0))
		held := deleteHeld(t, p, c, deleteCall)
		const want = `DELETE FROM "Smoke Table" WHERE "K""id" = 'O''Brien' AND k2 IS NULL`
		s := deleteSelect(deleteCall, want, `"Smoke Table"`, "1 2", int64(2), `{"O'Brien",NULL}`)
		generated := deleteRead(t, p, b, held, s)
		run(fixture.Exec(`INSERT INTO "Smoke Table" VALUES('O''Brien',NULL,'target'),('other',NULL,'decoy1'),('O''Brien','other','decoy2')`, 3))
		run(fixture.Exec(generated, 1))
		run(fixture.Select(`SELECT payload FROM "Smoke Table" ORDER BY payload`, []string{"payload"}, []string{"TEXT"}, [][]any{{"decoy1"}, {"decoy2"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, deleteError(deleteCall, "2202E", "target key array length must match number of key attributes", `"Smoke Table"`, "1 2", int64(2), "{a}"))
		run(fixture.Exec("CREATE TABLE delete_smoke_logical(gone text,id text)", 0))
		run(fixture.Exec("ALTER TABLE delete_smoke_logical DROP COLUMN gone", 0))
		logical := deleteSelect(deleteCall, "DELETE FROM delete_smoke_logical WHERE id = 'a'", "delete_smoke_logical", "1", int64(1), "{a}")
		deleteBoth(t, p, b, c, logical)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, logical)
		if b.PG { return }
		run(fixture.Exec("CREATE VIEW delete_smoke_saved AS SELECT dbdel_ext.dblink_build_sql_delete('delete_smoke_logical','1'::int2vector,1,'{a}'::text[]) AS sql", 0))
		saved := deleteSelect("SELECT sql FROM delete_smoke_saved", "DELETE FROM delete_smoke_logical WHERE id = 'a'")
		old, err := c.QueryContext(p.Ctx, saved.Query)
		if err != nil { t.Fatal(err) }
		defer old.Close()
		columns, err := old.Columns()
		if err != nil { t.Fatal(err) }
		types, err := old.ColumnTypes()
		if err != nil { t.Fatal(err) }
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		deleteBoth(t, p, b, c, saved)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, logical)
		deleteBoth(t, p, b, c, deleteMember("dblink_build_sql_delete", "dbdel_ext"))
		deleteOldText(t, old, columns, types, "DELETE FROM delete_smoke_logical WHERE id = 'a'")
	})
}
