package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"heap_control_reader"})
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO heap_t VALUES(7,'kept'),(NULL,NULL)", 2))
		run(fixture.Exec("CREATE ROLE heap_control_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION verify_heapam"+heapSignature+" TO heap_control_reader", 0))
		run(fixture.Exec("CREATE VIEW heap_saved AS SELECT * FROM verify_heapam(relation=>'heap_t',on_error_stop=>true)", 0))
		q := "SELECT * FROM verify_heapam($1::regclass)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, "heap_t"))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_saved"))
		// Both backends witness transaction-owned provider identity and rollback.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER FUNCTION verify_heapam"+heapSignature+" RENAME TO heap_temporary", 0))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_temporary('heap_t')"))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_saved"))
		run(fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, "heap_t"))
		if b.PG {
			run(fixture.Exec("DROP EXTENSION amcheck CASCADE", 0))
			run(fixture.Exec("CREATE EXTENSION amcheck", 0))
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
			return
		}
		// The native-only phase remains part of the public shared scenario;
		// PostgreSQL has no Dump/Reset/Restore API counterpart.
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='verify_heapam'").Scan(&sourceOID); err != nil { t.Fatal(err) }
		old, err := c.QueryContext(p.Ctx, "SELECT * FROM heap_saved")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		columns, err := old.Columns()
		if err != nil { t.Fatal(err) }
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		heapBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE proname='verify_heapam'", []string{"proname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil { t.Fatal(err) }
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_saved"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, "heap_t"))
		heapRetainedEmpty(t, old, columns)
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name()+"/destination")
		if err != nil { t.Fatal(err) }
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		temporary, err := recorded.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer temporary.Close()
		// Keep the actual colliding TEMP objects alive on their physical owner
		// through both failed and successful destination replacement.
		retained := heapRetainProcedureCollision(t, p, temporary, recorder, sourceOID)
		for _, query := range []string{"CREATE TABLE heap_acl_sentinel(v INT)", "INSERT INTO heap_acl_sentinel VALUES(19)"} {
			if _, err := destination.ExecContext(p.Ctx, query); err != nil { t.Fatal(query, err) }
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err == nil { t.Fatal("missing ACL grantee silently discarded") }
		var sentinel int64
		if err := destination.QueryRowContext(p.Ctx, "SELECT v FROM heap_acl_sentinel").Scan(&sentinel); err != nil || sentinel != 19 { t.Fatal("failed ACL replay replaced destination", sentinel, err) }
		if _, err := destination.ExecContext(p.Ctx, "CREATE ROLE heap_control_reader"); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil { t.Fatal(err) }
		var destinationOID int64
		if err := destination.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='verify_heapam'").Scan(&destinationOID); err != nil || destinationOID == sourceOID { t.Fatal("destination installed identity was not remapped", sourceOID, destinationOID, err) }
		for _, step := range []fixture.Step{
			heapEmpty("SELECT * FROM verify_heapam('heap_t')"), heapEmpty("SELECT * FROM heap_saved"),
			fixture.Select("SELECT a,b FROM heap_t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {nil, nil}}),
		} {
			for _, prepared := range []bool{false, true} {
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, recorded, recorder, step)
			}
		}
		if got := heapTemporaryIdentities(t, p, temporary); !reflect.DeepEqual(got, retained) { t.Fatal("Restore changed retained TEMP identities", got, retained) }
		fixture.RunStep(t, p.Ctx, temporary, recorder, fixture.Select("SELECT v FROM pg_temp.heap_retained_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		// Prove restored EXECUTE authority by invoking the actual member as its
		// reader on pinned source and destination sessions, without table SELECT.
		heapControlReader(t, p, b, c, true)
		destinationBackend := b
		destinationBackend.DB, destinationBackend.Recorder = recorded, recorder
		heapControlReader(t, p, destinationBackend, temporary, true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION verify_heapam"+heapSignature+" FROM heap_control_reader", 0))
		var revoked bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &revoked); err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(revoked.Bytes())); err != nil { t.Fatal(err) }
		heapControlReader(t, p, b, c, false)
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
	})
}

func heapRetainedEmpty(t *testing.T, rows *sql.Rows, columns []string) {
	t.Helper()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, columns) || !reflect.DeepEqual(got, []string{"blkno", "offnum", "attnum", "msg"}) { t.Fatal("retained columns", got, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 { t.Fatal("retained descriptors", types, err) }
	for i, name := range []string{"INT8", "INT4", "INT4", "TEXT"} {
		if types[i].DatabaseTypeName() != name { t.Fatal("retained type", i, types[i].DatabaseTypeName()) }
	}
	if rows.Next() || rows.Err() != nil { t.Fatal("retained healthy findings", rows.Err()) }
}

// Only an actual occupied retained identity warrants a remint assertion. The
// temporary namespace pair, table, row type and array all participate in the
// native collision mapper's global numeric OID exclusion set.
func heapRetainProcedureCollision(t *testing.T, p *fixture.Pair, owner *sql.Conn, recorder *fixture.Recorder, target int64) map[string]int64 {
	t.Helper()
	if target <= 0 { t.Fatal("invalid source procedure identity", target) }
	var previous int64
	for serial := 0; ; serial++ {
		fixture.RunStep(t, p.Ctx, owner, recorder, fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE heap_retained_%d(v INT)", serial), 0))
		identities := heapTemporaryIdentities(t, p, owner)
		var maximum int64
		collides := false
		for _, id := range identities { maximum = max(maximum, id); collides = collides || id == target }
		if collides {
			fixture.RunStep(t, p.Ctx, owner, recorder, fixture.Exec("INSERT INTO pg_temp.heap_retained_0 VALUES(73)", 1))
			return identities
		}
		if maximum <= previous || maximum >= target { t.Fatal("retained TEMP allocation did not cover source procedure identity", target, identities) }
		previous = maximum
	}
}

func heapTemporaryIdentities(t *testing.T, p *fixture.Pair, owner *sql.Conn) map[string]int64 {
	t.Helper()
	identities := map[string]int64{}
	for _, part := range []struct { table, name, filter string }{
		{"pg_class", "relname", "relnamespace=pg_my_temp_schema()"},
		{"pg_type", "typname", "typnamespace=pg_my_temp_schema()"},
		{"pg_namespace", "nspname", "(nspname LIKE 'pg_temp_%' OR nspname LIKE 'pg_toast_temp_%') AND NOT pg_is_other_temp_schema(oid)"},
	} {
		rows, err := owner.QueryContext(p.Ctx, "SELECT "+part.name+",oid::bigint FROM "+part.table+" WHERE "+part.filter+" ORDER BY oid")
		if err != nil { t.Fatal(err) }
		for rows.Next() {
			var name string
			var id int64
			if err := rows.Scan(&name, &id); err != nil { rows.Close(); t.Fatal(err) }
			if id <= 0 { rows.Close(); t.Fatal("invalid retained TEMP identity", name, id) }
			identities[part.table+"/"+name] = id
		}
		readErr, closeErr := rows.Err(), rows.Close()
		if readErr != nil || closeErr != nil { t.Fatal(readErr, closeErr) }
	}
	return identities
}
