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

func TestReferencePgFreespaceControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_control_owner", "fs_control_reader"})
	fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE fs_control_owner", "CREATE ROLE fs_control_reader",
			"GRANT USAGE ON SCHEMA fs_part TO fs_control_owner,fs_control_reader",
			"ALTER TABLE fs_part.heap OWNER TO fs_control_owner",
			"GRANT pg_stat_scan_tables TO fs_control_reader",
			"ALTER FUNCTION fs_part.pg_freespace(regclass,bigint) OWNER TO fs_control_owner",
			"GRANT EXECUTE ON FUNCTION fs_part.pg_freespace(regclass) TO fs_control_reader",
			"CREATE VIEW fs_part.scalar_view AS SELECT fs_part.pg_freespace('fs_part.heap'::regclass,0) AS avail",
			"CREATE VIEW fs_part.source_view AS SELECT * FROM fs_part.pg_freespace('fs_part.heap'::regclass)",
			"CREATE TABLE fs_part.consumer(v smallint DEFAULT fs_part.pg_freespace('fs_part.heap'::regclass,0),CHECK(v>=0 AND fs_part.pg_freespace('fs_part.heap'::regclass,0)>=0))",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
		avail := int64(8096)
		if b.PG {
			avail = 0
		}
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, target, rec, s)
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Prepared(s))
			}
			run(fsBodies("fs_part"))
			run(fixture.Select("SELECT avail FROM fs_part.scalar_view", []string{"avail"}, []string{"INT2"}, [][]any{{avail}}))
			run(fixture.Select("SELECT * FROM fs_part.source_view ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), avail}}))
			run(fixture.Select("SELECT k FROM fs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='fs_part.heap'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"fs_control_owner"}}))
			run(fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='fs_part.pg_freespace(regclass,bigint)'::regprocedure", []string{"owner"}, []string{"NAME"}, [][]any{{"fs_control_owner"}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("SET SESSION AUTHORIZATION fs_control_reader", 0))
			run(fixture.Select("SELECT fs_part.pg_freespace('fs_part.heap',0) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{avail}}))
			run(fixture.Select("SELECT * FROM fs_part.pg_freespace('fs_part.heap') ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), avail}}))
			run(fsError("SELECT k FROM fs_part.heap", "42501", "permission denied for table heap"))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='fs_part.pg_freespace(regclass)'::regprocedure AND refclassid='pg_proc'::regclass AND refobjid='fs_part.pg_freespace(regclass,bigint)'::regprocedure AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post(c, b.Recorder)
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.consumer DEFAULT VALUES", 1))
		fsBoth(t, p, b, c, fixture.Select("SELECT v FROM fs_part.consumer", []string{"v"}, []string{"INT2"}, [][]any{{avail}}))
		// Shared SQL/identity/ACL witnesses above run on both engines. These
		// public controls deliberately have no PostgreSQL equivalent.
		if b.PG {
			return
		}
		original := fsOID(t, p, c, "fs_part.heap")
		queries := []string{"SELECT fs_part.pg_freespace($1::regclass,$2::bigint) AS avail", "SELECT * FROM fs_part.pg_freespace($1::regclass) ORDER BY blkno"}
		held := make([]fixture.HeldStatement, len(queries))
		for i, q := range queries {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		readHeld := func() {
			fixture.RunStep(t, p.Ctx, held[0], b.Recorder, fixture.Select(queries[0], []string{"avail"}, []string{"INT2"}, [][]any{{int64(8096)}}, "fs_part.heap", int64(0)))
			fixture.RunStep(t, p.Ctx, held[1], b.Recorder, fixture.Select(queries[1], fsNames, fsTypes, [][]any{{int64(0), int64(8096)}}, "fs_part.heap"))
		}
		readHeld()
		// Finish the private writer before borrowing retained output. Dump must
		// exclude its unpublished row, without a retained reader sharing a writer.
		dump := func() string {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer pvRollback(peer)
			fsRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
			fsRun(t, p, b, peer, fixture.Exec("INSERT INTO fs_part.heap VALUES(99)", 1))
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			fsRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
			return dump
		}()
		retained, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		old, err := retained.QueryContext(p.Ctx, "SELECT * FROM fs_part.source_view ORDER BY blkno")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if !old.Next() {
			t.Fatal("retained rows lost", old.Err())
		}
		var block, free int64
		if err := old.Scan(&block, &free); err != nil || block != 0 || free != 8096 {
			t.Fatal("retained values", block, free, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		if len(metadata) != 2 || metadata[0].Name() != "blkno" || metadata[0].DatabaseTypeName() != "INT8" || metadata[1].Name() != "avail" || metadata[1].DatabaseTypeName() != "INT2" {
			t.Fatal("retained metadata", metadata)
		}
		// The old output survives Reset, but neither it nor its borrower remains
		// live during fresh queries, Restore, destination DML or fixture cleanup.
		fsBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_freespacemap'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		readHeld()
		fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.consumer DEFAULT VALUES", 1))
		fsBoth(t, p, b, c, fixture.Select("SELECT DISTINCT v FROM fs_part.consumer", []string{"v"}, []string{"INT2"}, [][]any{{int64(8096)}}))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		for _, bad := range []string{dump + "\nINVALID SQL;", dump + "\nCREATE EXTENSION nonexistent_provider;", dump + "\nALTER TABLE fs_part.heap OWNER TO fs_missing_role;", strings.Replace(dump, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)} {
			if bad == dump {
				t.Fatal("corrupt fixture unchanged")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid restore accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("rejected restore published prefix")
			}
			post(c, b.Recorder)
			readHeld()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore published prefix")
		}
		post(c, b.Recorder)
		readHeld()
		ioErr := errors.New("freespace restore read failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fsControlReader{err: ioErr}); !errors.Is(err, ioErr) {
			t.Fatal("ordinary I/O error", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("I/O failure published prefix")
		}
		post(c, b.Recorder)
		readHeld()
		connector, err := stuntgres.NewDriver().OpenConnector("freespace-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, rec := fixture.RecordedNativePool(t, connector)
		dest, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		defer dest.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		runDest := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, dest, rec, s) }
		runDest(fixture.Exec("CREATE TABLE destination_collision(k integer)", 0))
		// Advance the destination's genuine relation allocator beyond the
		// imported identity, so the remap assertion is a witness, not luck.
		for last, n := fsOID(t, p, dest, "destination_collision"), 0; last <= original; n++ {
			name := fmt.Sprintf("destination_pad_%d", n)
			runDest(fixture.Exec("CREATE TABLE "+name+"(k integer)", 0))
			last = fsOID(t, p, dest, name)
		}
		runDest(fixture.Exec("CREATE TEMP TABLE fs_keep(k integer)", 0))
		runDest(fixture.Exec("INSERT INTO fs_keep VALUES(19)", 1))
		destBefore := fixture.ExtensionDump(t, p.Ctx, destination)
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err == nil {
			t.Fatal("restore invented destination owners")
		}
		if fixture.ExtensionDump(t, p.Ctx, destination) != destBefore {
			t.Fatal("missing owner published prefix")
		}
		for _, q := range []string{"CREATE ROLE fs_control_owner", "CREATE ROLE fs_control_reader", "GRANT pg_stat_scan_tables TO fs_control_reader"} {
			runDest(fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		if got := fsOID(t, p, dest, "fs_part.heap"); got == original {
			t.Fatal("destination did not exercise OID remapping", got)
		}
		post(dest, rec)
		runDest(fixture.Select("SELECT k FROM fs_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		runDest(fixture.Exec("INSERT INTO fs_part.consumer DEFAULT VALUES", 1))
		runDest(fixture.Select("SELECT DISTINCT v FROM fs_part.consumer", []string{"v"}, []string{"INT2"}, [][]any{{int64(8096)}}))
		runDest(fixture.Exec("TRUNCATE fs_part.heap", 0))
		runDest(fixture.Select("SELECT * FROM fs_part.pg_freespace('fs_part.heap') ORDER BY blkno", fsNames, fsTypes, nil))
		runDest(fixture.Select("SELECT avail FROM fs_part.scalar_view", []string{"avail"}, []string{"INT2"}, [][]any{{int64(0)}}))
		post(c, b.Recorder)
		readHeld()
	})
}

type fsControlReader struct{ err error }

func (r fsControlReader) Read([]byte) (int, error) { return 0, r.err }
