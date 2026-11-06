package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pw_control_owner", "pw_control_reader"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pwCacheInstall(t, p, b, c)
		bcDDL(t, p, b, c,
			"CREATE ROLE pw_control_owner", "CREATE ROLE pw_control_reader",
			"GRANT USAGE ON SCHEMA pw_part TO pw_control_owner,pw_control_reader",
			"ALTER TABLE pw_part.heap OWNER TO pw_control_owner",
			"ALTER FUNCTION "+pwSignature+" OWNER TO pw_control_owner",
			"REVOKE EXECUTE ON FUNCTION "+pwSignature+" FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION "+pwSignature+" TO pw_control_reader",
			"GRANT SELECT ON pw_part.heap TO pw_control_reader",
			"CREATE VIEW pw_part.bound AS SELECT pw_part.pg_prewarm('pw_part.heap'::regclass) AS warmed",
			"CREATE TABLE pw_part.consumer(v bigint DEFAULT pw_part.pg_prewarm('pw_part.heap'::regclass),CHECK(v>=0 AND pw_part.pg_prewarm('pw_part.heap'::regclass)>=0))")
		pwInsert(t, p, b, c, "pw_part.heap", 1)
		bcDDL(t, p, b, c, "VACUUM (FREEZE) pw_part.heap")
		post := func(target *sql.Conn, backend fixture.Backend) {
			run := func(s fixture.Step) { bcBoth(t, p, backend, target, s) }
			run(pwViewCount("SELECT warmed FROM pw_part.bound", 1, backend.PG))
			run(fixture.Select("SELECT k FROM pw_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT pg_get_userbyid(proowner) AS owner,pronargdefaults::integer AS defaults,proargtypes::text AS args FROM pg_proc WHERE oid=$1::regprocedure", []string{"owner", "defaults", "args"}, []string{"NAME", "INT4", "TEXT"}, [][]any{{"pw_control_owner", int64(4), "2205 25 25 20 20"}}, pwSignature))
			run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='pw_part.heap'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"pw_control_owner"}}))
			for _, fork := range []string{"fsm", "vm"} {
				n := pwExtent(t, p, backend, target, "pw_part.heap", fork, 1)
				if n < 1 { t.Fatal("restored auxiliary provenance absent", fork, n) }
				run(pwCount(pwCall, n, "pw_part.heap", "buffer", fork, nil, nil))
			}
			bcDDL(t, p, backend, target, "SET SESSION AUTHORIZATION pw_control_reader")
			run(pwCount(pwCall, 1, "pw_part.heap", "buffer", "main", nil, nil))
			bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION")
			bcRun(t, p, backend, target, fixture.Exec("INSERT INTO pw_part.consumer DEFAULT VALUES", 1))
			run(fixture.Select("SELECT DISTINCT v FROM pw_part.consumer", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post(c, b)
		if b.PG { return }
		databases = append(databases, "pw_control_other")
		bcDDL(t, p, b, c, "CREATE DATABASE pw_control_other")
		otherPool, _, otherRecorder := fixture.DatabaseScenarioPool(t, p, false, "pw_control_other", "")
		other, err := otherPool.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer other.Close()
		ob := fixture.Backend{Name: "other_database", DB: otherPool, Recorder: otherRecorder}
		bcDDL(t, p, ob, other, "CREATE SCHEMA pw_part", "CREATE EXTENSION pg_prewarm SCHEMA pw_part", "CREATE TABLE pw_part.heap(k integer)")
		pwCacheInstall(t, p, ob, other)
		pwInsert(t, p, ob, other, "pw_part.heap", 1000)
		bcRun(t, p, ob, other, pwCount(pwCall, 2, "pw_part.heap", "buffer", "main", int64(1), int64(2)))
		peerCoordinates := pwCoordinates(t, p, ob, other, "pw_part.heap")
		pwResident(t, p, ob, other, "pw_part.heap", 0, 1, 2)
		original := fsOID(t, p, c, "pw_part.heap")
		st, err := c.PrepareContext(p.Ctx, pwCall)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: pwCall}
		readHeld := func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, pwCount(pwCall, 1, "pw_part.heap", "buffer", "main", nil, nil)) }
		readHeld()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		// Retained count and cache rows must be detached from the roots that
		// Reset retires. Close them before any restored invocation.
		retained, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer retained.Close()
		oldCount, err := retained.QueryContext(p.Ctx, pwCall, "pw_part.heap", "buffer", "main", nil, nil)
		if err != nil { t.Fatal(err) }
		defer oldCount.Close()
		countMeta, err := oldCount.ColumnTypes()
		if err != nil { t.Fatal(err) }
		node, space, database := bcStorage(t, p, c, "pw_part.heap")
		cacheOwner, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer cacheOwner.Close()
		oldCache, err := cacheOwner.QueryContext(p.Ctx, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", node, space, database)
		if err != nil { t.Fatal(err) }
		defer oldCache.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		pwSameCoordinates(t, peerCoordinates, pwCoordinates(t, p, ob, other, "pw_part.heap"))
		if !oldCount.Next() { t.Fatal("detached count lost", oldCount.Err()) }
		var count int64
		if err := oldCount.Scan(&count); err != nil || count != 1 { t.Fatal(count, err) }
		if oldCount.Next() || oldCount.Err() != nil { t.Fatal("detached count tail", oldCount.Err()) }
		if len(countMeta) != 1 || countMeta[0].Name() != "warmed" || countMeta[0].DatabaseTypeName() != "INT8" { t.Fatal("retained count metadata", countMeta) }
		if err := oldCount.Close(); err != nil { t.Fatal(err) }
		cacheRows := bcRows(t, oldCache, false, true, bcNames, bcTypes)
		if len(cacheRows) < 3 { t.Fatal("detached main/fsm/vm lost", cacheRows) }
		if err := retained.Close(); err != nil { t.Fatal(err) }
		if err := cacheOwner.Close(); err != nil { t.Fatal(err) }
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_prewarm'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		if len(pwCoordinates(t, p, b, c, "pw_part.heap")) != 0 { t.Fatal("Restore serialized live occupancy") }
		pwSameCoordinates(t, peerCoordinates, pwCoordinates(t, p, ob, other, "pw_part.heap"))
		readHeld()
		pwResident(t, p, b, c, "pw_part.heap", 0, 0, 0)
		post(c, b)
		bcDDL(t, p, b, c, "REVOKE SELECT ON pw_part.heap FROM pw_control_reader", "SET SESSION AUTHORIZATION pw_control_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pwError(pwCall, "42501", "permission denied for table heap", "pw_part.heap", "buffer", "main", nil, nil))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON pw_part.heap TO pw_control_reader")
		readHeld()
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		resident := pwCoordinates(t, p, b, c, "pw_part.heap")
		for _, bad := range []string{dump+"\nINVALID SQL;", dump+"\nCREATE EXTENSION nonexistent_provider;", dump+"\nALTER TABLE pw_part.heap OWNER TO missing_pw_owner;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil { t.Fatal("invalid restore accepted") }
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("failed replacement published prefix") }
			pwSameCoordinates(t, resident, pwCoordinates(t, p, b, c, "pw_part.heap"))
			pwSameCoordinates(t, peerCoordinates, pwCoordinates(t, p, ob, other, "pw_part.heap"))
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) { t.Fatal("canceled restore", err) }
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("canceled replacement published prefix") }
		pwSameCoordinates(t, resident, pwCoordinates(t, p, b, c, "pw_part.heap"))
		connector, err := stuntgres.NewDriver().OpenConnector("prewarm-independent-destination")
		if err != nil { t.Fatal(err) }
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, rec := fixture.RecordedNativePool(t, connector)
		dest, err := pool.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer dest.Close()
		db := fixture.Backend{Name: "destination", DB: pool, Recorder: rec}
		bcDDL(t, p, db, dest, "CREATE ROLE pw_control_owner", "CREATE ROLE pw_control_reader", "CREATE TABLE destination_collision(k integer)")
		for last, i := fsOID(t, p, dest, "destination_collision"), 0; last <= original; i++ {
			name := fmt.Sprintf("destination_pad_%d", i)
			bcDDL(t, p, db, dest, "CREATE TABLE "+name+"(k integer)")
			last = fsOID(t, p, dest, name)
		}
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		if fsOID(t, p, dest, "pw_part.heap") == original { t.Fatal("destination did not exercise OID remapping") }
		if len(pwCoordinates(t, p, db, dest, "pw_part.heap")) != 0 { t.Fatal("independent destination not cold") }
		post(dest, db)
		pwResident(t, p, db, dest, "pw_part.heap", 0, 0, 0)
		bcDDL(t, p, db, dest, "TRUNCATE pw_part.heap")
		bcRun(t, p, db, dest, pwViewCount("SELECT warmed FROM pw_part.bound", 0, false))
		readHeld()
	})
}
