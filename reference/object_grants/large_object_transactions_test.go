package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTransactions(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910051, 910052, 910053)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observe := func(s fixture.Step) {
			t.Helper()
			s.Prepared = prepared
			objectGrantStep(t, p.Ctx, peer, b.Recorder, s)
		}
		metadata := func(oid string, present bool) fixture.Step {
			var rows [][]any
			if present {
				rows = [][]any{{true, nil}}
			}
			return fixture.Select("SELECT lomowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned,lomacl FROM pg_largeobject_metadata WHERE oid="+oid+"::oid", []string{"owned", "lomacl"}, []string{"BOOL", "_ACLITEM"}, rows)
		}
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectValue("lo_from_bytea(910051::oid,decode('6162','hex'))", "OID", int64(910051)))
		run(largeObjectBytes(910051, "6162"))
		observe(largeObjectMissing("SELECT lo_get(910051::oid)", 910051))
		observe(metadata("910051", false))
		run(fixture.Exec("COMMIT", 0))
		observe(largeObjectBytes(910051, "6162"))
		observe(metadata("910051", true))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectPut(910051, 1, "00ff"))
		run(largeObjectBytes(910051, "6100ff"))
		observe(largeObjectBytes(910051, "6162"))
		run(fixture.Exec("ROLLBACK", 0))
		run(largeObjectBytes(910051, "6162"))
		observe(largeObjectBytes(910051, "6162"))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectPut(910051, 0, "6364"))
		run(fixture.Exec("SAVEPOINT lo_before_second_write", 0))
		run(largeObjectPut(910051, 0, "ffff"))
		run(largeObjectBytes(910051, "ffff"))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT lo_before_second_write", 0))
		run(largeObjectBytes(910051, "6364"))
		observe(largeObjectBytes(910051, "6162"))
		run(fixture.Exec("COMMIT", 0))
		observe(largeObjectBytes(910051, "6364"))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectValue("lo_create(910052::oid)", "OID", int64(910052)))
		run(fixture.Exec("ROLLBACK", 0))
		observe(largeObjectMissing("SELECT lo_get(910052::oid)", 910052))
		run(largeObjectValue("lo_create(910052::oid)", "OID", int64(910052)))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectValue("lo_unlink(910051::oid)", "INT4", int64(1)))
		run(metadata("910051", false))
		observe(largeObjectBytes(910051, "6364"))
		run(fixture.Exec("ROLLBACK", 0))
		run(largeObjectBytes(910051, "6364"))
		run(metadata("910051", true))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectValue("lo_unlink(910051::oid)", "INT4", int64(1)))
		observe(largeObjectBytes(910051, "6364"))
		run(fixture.Exec("COMMIT", 0))
		for _, q := range []string{"SELECT lo_get(910051::oid)", "SELECT lo_put(910051::oid,0::bigint,decode('','hex'))", "SELECT lo_unlink(910051::oid)"} {
			observe(largeObjectMissing(q, 910051))
		}
		// PG18.6 does not expose this nested creation to the outer lo_get.
		// The resulting statement error rolls back the newly created object.
		run(largeObjectMissing("SELECT encode(lo_get(lo_from_bytea(910053::oid,decode('00ff','hex'))),'hex') AS v", 910053))
		run(largeObjectMissing("SELECT lo_get(910053::oid)", 910053))
		observe(largeObjectMissing("SELECT lo_get(910053::oid)", 910053))
		run(metadata("910053", false))
		observe(metadata("910053", false))
	})
}

func TestReferenceLargeObjectPrepared(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910061, 910062)
		createSQL := "SELECT lo_create($1::oid) AS v"
		create, err := c.PrepareContext(p.Ctx, createSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		run(largeObjectMissing("SELECT lo_get(910061::oid)", 910061))
		heldCreate := fixture.HeldStatement{Executor: c, Statement: create, SQL: createSQL}
		fixture.RunStep(t, p.Ctx, heldCreate, b.Recorder, largeObjectBackendStep(largeObjectValue("lo_create($1::oid)", "OID", int64(910061), int64(910061)), b))
		// Preparing an already-duplicate fixed OID remains quiet.
		duplicate, err := c.PrepareContext(p.Ctx, "SELECT lo_create(910061::oid)")
		if err != nil {
			t.Fatal(err)
		}
		defer duplicate.Close()
		duplicateStep := fixture.ImplicitError("SELECT lo_create(910061::oid)", fixture.Diagnostic{
			Code: "23505", Message: `duplicate key value violates unique constraint "pg_largeobject_metadata_oid_index"`,
			Detail: "Key (oid)=(910061) already exists.", Schema: "pg_catalog", Table: "pg_largeobject_metadata", Constraint: "pg_largeobject_metadata_oid_index",
		})
		duplicateStep.SelectRows = true
		duplicateStep.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: duplicate, SQL: duplicateStep.Query}, b.Recorder, duplicateStep)
		getSQL := "SELECT lo_get($1::oid,$2::bigint,$3::integer) AS v"
		putSQL := "SELECT lo_put($1::oid,$2::bigint,$3::bytea) AS v"
		get, err := c.PrepareContext(p.Ctx, getSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer get.Close()
		put, err := c.PrepareContext(p.Ctx, putSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer put.Close()
		getHeld := fixture.HeldStatement{Executor: c, Statement: get, SQL: getSQL}
		putHeld := fixture.HeldStatement{Executor: c, Statement: put, SQL: putSQL}
		read := largeObjectValue("lo_get($1::oid,$2::bigint,$3::integer)", "BYTEA", string([]byte{0, 255}), int64(910061), int64(0), int64(2))
		write := largeObjectValue("lo_put($1::oid,$2::bigint,$3::bytea)", "VOID", "", int64(910061), int64(0), []byte{0, 255})
		fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, largeObjectValue("lo_get($1::oid,$2::bigint,$3::integer)", "BYTEA", "", int64(910061), int64(0), int64(2)))
		run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910061 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RunStep(t, p.Ctx, putHeld, b.Recorder, write)
			fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, read)
		})
		run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 910061 FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, largeObjectError(getSQL, "42501", "permission denied for large object 910061", int64(910061), int64(0), int64(2)))
			fixture.RunStep(t, p.Ctx, putHeld, b.Recorder, largeObjectError(putSQL, "42501", "permission denied for large object 910061", int64(910061), int64(0), []byte{}))
		})
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910061 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, read) })
		run(largeObjectValue("lo_unlink(910061::oid)", "INT4", int64(1)))
		fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, largeObjectMissing(getSQL, 910061, int64(910061), int64(0), int64(2)))
		run(largeObjectValue("lo_from_bytea(910061::oid,decode('7a','hex'))", "OID", int64(910061)))
		fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, largeObjectValue("lo_get($1::oid,$2::bigint,$3::integer)", "BYTEA", "z", int64(910061), int64(0), int64(2)))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, largeObjectError(getSQL, "42501", "permission denied for large object 910061", int64(910061), int64(0), int64(2)))
		})
		unlink, err := c.PrepareContext(p.Ctx, "SELECT lo_unlink(910061::oid) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer unlink.Close()
		run(largeObjectBytes(910061, "7a"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: unlink, SQL: "SELECT lo_unlink(910061::oid) AS v"}, b.Recorder, largeObjectValue("lo_unlink(910061::oid)", "INT4", int64(1)))
		// Independently released pg_proc OIDs and strict/volatile/unsafe flags.
		run(fixture.Select("SELECT oid,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid IN (715,957,964,3457,3458,3459,3460) ORDER BY oid", []string{"oid", "proisstrict", "provolatile", "proparallel"}, []string{"OID", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(715), true, "v", "u"}, {int64(957), true, "v", "u"}, {int64(964), true, "v", "u"}, {int64(3457), true, "v", "u"}, {int64(3458), true, "v", "u"}, {int64(3459), true, "v", "u"}, {int64(3460), true, "v", "u"}}))
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.lo_get(oid)"})
		run(largeObjectValue("lo_create(910062::oid)", "OID", int64(910062)))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910062 TO og_object_reader", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.lo_get(oid) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT lo_get(910062::oid)", "function", "lo_get") })
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.lo_get(oid) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(largeObjectBytes(910062, "")) })
	})
}
