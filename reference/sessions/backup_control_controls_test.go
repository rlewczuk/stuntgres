package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBackupControlNativeControls(t *testing.T) {
	const role = "backup_control_reader"
	fixture.RoleReferenceCleanup(t, []string{role, "backup_control_collision"})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer backupRestore(t, p, b, c, role)()
		run(fixture.Exec("CREATE ROLE backup_control_collision", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE TABLE backup_control_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO backup_control_sentinel VALUES(7)", 1))
		run(fixture.Exec("CREATE VIEW backup_control_start AS SELECT pg_backup_start(fast=>false,label=>NULL::text) AS marker", 0))
		run(fixture.Exec("CREATE VIEW backup_control_stop AS SELECT * FROM pg_backup_stop(NULL::boolean)", 0))
		run(fixture.Exec("GRANT SELECT ON backup_control_start,backup_control_stop TO "+role, 0))
		view := backupSelect(b.PG, "SELECT marker FROM backup_control_start", []string{"marker"}, []string{"PG_LSN"}, [][]any{{nil}})
		stopView := backupSelect(b.PG, "SELECT * FROM backup_control_stop", backupFields, backupTypes, [][]any{{nil, nil, nil}})
		view.CheckMetadata = backupMetadata(b.PG, true, view.Columns, view.Types)
		stopView.CheckMetadata = backupMetadata(b.PG, true, backupFields, backupTypes)
		backupRoutes(t, p, b, c, view)
		backupRoutes(t, p, b, c, stopView)
		run(backupInitial(b.PG))
		var baseline bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if baseline.String() != again.String() {
				t.Fatal("unstable seed dump")
			}
			for _, id := range backupIdentities {
				if strings.Contains(baseline.String(), "WHERE oid='pg_catalog."+id+"'::pg_catalog.regprocedure") {
					t.Fatal("gratuitous bootstrap repair")
				}
			}
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_start(text,boolean) TO "+role+" WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_stop(boolean) TO "+role, 0))
		run(fixture.Select("SELECT has_function_privilege('"+role+"','pg_backup_start(text,boolean)','EXECUTE WITH GRANT OPTION') AS start_option,has_function_privilege('"+role+"','pg_backup_stop(boolean)','EXECUTE WITH GRANT OPTION') AS stop_option", []string{"start_option", "stop_option"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(backupInitial(b.PG))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		rawGrants := fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{"{" + owner + "=X/" + owner + "," + role + "=X*/" + owner + "}"}, {"{" + owner + "=X/" + owner + "," + role + "=X/" + owner + "}"}})
		run(rawGrants)
		var granted bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &granted); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(granted.String())); err != nil {
				t.Fatal(err)
			}
			run(rawGrants)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if granted.String() != again.String() {
				t.Fatal("grant option lost")
			}
		}
		// Native catalog writes are admitted only by staged logical replay.
		setRaw := func(start, stop string) {
			if b.PG {
				run(fixture.Exec("UPDATE pg_catalog.pg_proc SET proacl="+start+" WHERE oid=2172", 1))
				run(fixture.Exec("UPDATE pg_catalog.pg_proc SET proacl="+stop+" WHERE oid=2739", 1))
				return
			}
			var d bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &d); err != nil {
				t.Fatal(err)
			}
			d.WriteString("\nUPDATE pg_catalog.pg_proc SET proacl=" + start + " WHERE oid='pg_catalog.pg_backup_start(text,boolean)'::pg_catalog.regprocedure;\nUPDATE pg_catalog.pg_proc SET proacl=" + stop + " WHERE oid='pg_catalog.pg_backup_stop(boolean)'::pg_catalog.regprocedure;\n")
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(d.String())); err != nil {
				t.Fatal(err)
			}
		}
		// Remove ordinary dependencies before raw replacement on stock PostgreSQL.
		for _, id := range backupIdentities {
			run(fixture.Exec("REVOKE ALL ON FUNCTION "+id+" FROM "+role, 0))
		}
		// Ordinary owner REVOKE removes the last ACL item without discarding its
		// one-dimensional ACL header (REL_18_6 acl.c:allocacl/aclupdate).
		run(fixture.Exec("REVOKE ALL ON FUNCTION pg_backup_stop(boolean) FROM "+owner, 0))
		run(fixture.Select("SELECT proacl::text AS acl,has_function_privilege('"+role+"','pg_backup_stop(boolean)','EXECUTE') AS allowed FROM pg_proc WHERE oid=2739", []string{"acl", "allowed"}, []string{"TEXT", "BOOL"}, [][]any{{"{}", false}}))
		// Both backends use the canonical empty-ACL constructor from WAL controls
		// and native Dump. A raw '{}' array cast has invalid stock dimensions
		// and is not generated native repair syntax; the stored raw ACL remains '{}'.
		empty := `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[]`
		setRaw("NULL::pg_catalog.aclitem[]", empty)
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(backupInitial(b.PG))
			run(fixture.Select("SELECT oid::bigint AS oid,proacl::text AS acl FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"oid", "acl"}, []string{"INT8", "TEXT"}, [][]any{{int64(2172), nil}, {int64(2739), "{}"}}))
			run(fixture.Select("SELECT v FROM backup_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(view)
			run(stopView)
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(view)
			for i, id := range backupIdentities {
				run(fixture.Select("SELECT has_function_privilege(current_user,'"+id+"','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{i == 0}}))
			}
			run(recoveryControlDenied("SELECT pg_backup_stop(NULL)", "pg_backup_stop"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if !b.PG {
				run(backupUnsupported("SELECT pg_backup_start('control',true)", "pg_backup_start"))
				run(backupUnsupported("SELECT pg_backup_stop(false)", "pg_backup_stop"))
			}
		}
		check(c, b.Recorder)
		if b.PG {
			setRaw("'{=X/stuntgres,stuntgres=X/stuntgres}'::pg_catalog.aclitem[]", "'{stuntgres=X/stuntgres}'::pg_catalog.aclitem[]")
			run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{"{=X/stuntgres,stuntgres=X/stuntgres}"}, {recoveryControlOwnerACL(true)}}))
			run(backupInitial(true))
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := c.QueryContext(p.Ctx, stopView.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stopView.CheckMetadata(t, retained)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(backupInitial(false))
		run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{recoveryControlOwnerACL(false)}, {recoveryControlOwnerACL(false)}}))
		run(backupUnsupported("SELECT pg_backup_start('reset',true)", "pg_backup_start"))
		run(backupUnsupported("SELECT pg_backup_stop(false)", "pg_backup_stop"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check(c, b.Recorder)
		if len(metadata) != 3 || !retained.Next() {
			t.Fatal("retained NULL record", metadata, retained.Err())
		}
		for i, col := range metadata {
			if col.Name() != backupFields[i] || col.DatabaseTypeName() != backupTypes[i] {
				t.Fatal("retained metadata", metadata)
			}
		}
		var a, z, w any
		if err := retained.Scan(&a, &z, &w); err != nil || a != nil || z != nil || w != nil {
			t.Fatal("retained typed NULLs", a, z, w, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal(retained.Err())
		}
		retained.Close()
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("raw ACL/view roundtrip not canonical")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nINSERT INTO backup_control_sentinel VALUES(99);\nSELECT no_backup_restore_function();\n")); err == nil {
			t.Fatal("accepted invalid replay")
		}
		check(c, b.Recorder)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed replay published state")
		}
		connector, err := stuntgres.NewDriver().OpenConnector("backup_destination")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		if _, err := control.ExecContext(p.Ctx, "CREATE ROLE backup_control_collision"); err != nil {
			t.Fatal(err)
		}
		if _, err := control.ExecContext(p.Ctx, "CREATE ROLE "+role); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(granted.String())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, recorded, recorder, rawGrants)
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT has_function_privilege('"+role+"','pg_backup_start(text,boolean)','EXECUTE WITH GRANT OPTION') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		var sourceRole, destinationRole int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", role).Scan(&sourceRole); err != nil {
			t.Fatal(err)
		}
		if err := control.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", role).Scan(&destinationRole); err != nil {
			t.Fatal(err)
		}
		if sourceRole == destinationRole {
			t.Fatal("destination did not exercise role identity remapping")
		}
		dest, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		check(dest, recorder)
		var imported bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, control, &imported); err != nil {
			t.Fatal(err)
		}
		if imported.String() != dump.String() {
			t.Fatal("destination principal remap changed logical dump")
		}
		// Explicit default-equivalent PUBLIC is distinct from raw NULL and seed.
		setRaw("'{=X/postgres,postgres=X/postgres}'::pg_catalog.aclitem[]", "'{postgres=X/postgres}'::pg_catalog.aclitem[]")
		var explicit bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &explicit); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(explicit.String())); err != nil {
			t.Fatal(err)
		}
		var explicitAgain bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &explicitAgain); err != nil {
			t.Fatal(err)
		}
		if explicit.String() != explicitAgain.String() {
			t.Fatal("explicit default ACL lost")
		}
		run(backupInitial(false))
		run(fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{"{=X/postgres,postgres=X/postgres}"}, {recoveryControlOwnerACL(false)}}))
		run(fixture.Select("SELECT has_function_privilege('"+role+"','pg_backup_start(text,boolean)','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
