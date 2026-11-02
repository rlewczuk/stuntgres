package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgFreespaceFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_smoke_reader"})
	fsScenario(t, "fs_smoke", true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE fs_smoke.heap(k integer)", "ALTER TABLE fs_smoke.heap SET(autovacuum_enabled=false)", "CREATE TABLE fs_smoke.empty(k integer)"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		q := "SELECT fs_smoke.pg_freespace($1::regclass,$2::bigint) AS avail"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		probe := func(target any, block int64, value any) {
			s := fsMetadata(fixture.Select(q, []string{"avail"}, []string{"INT2"}, [][]any{{value}}, target, block), b.PG)
			fsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT * FROM fs_smoke.pg_freespace('fs_smoke.heap') ORDER BY blkno", fsNames, fsTypes, nil), b.PG))
		probe("fs_smoke.heap", 0, int64(0))
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_smoke.heap VALUES(1)", 1))
		one, two := int64(8096), int64(8064)
		// On PostgreSQL this fresh never-vacuumed heap has no FSM entry. Its
		// physical page domain is independently checked below, not equated to
		// the native compact-row allocator or learned from another backend.
		if b.PG {
			one = 0
			two = 0
		}
		probe("fs_smoke.heap", 0, one)
		fsRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO fs_smoke.heap VALUES($1)", 1, int64(2))))
		probe("fs_smoke.heap", 0, two)
		fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_smoke.heap WHERE k=2", 1))
		probe("fs_smoke.heap", 0, one)
		fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT * FROM fs_smoke.pg_freespace('fs_smoke.heap') ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), one}}), b.PG))
		if b.PG {
			fsBoth(t, p, b, c, fixture.Select("SELECT pg_relation_size('fs_smoke.heap')=8192 AS one_page,8192=current_setting('block_size')::bigint AS page_size", []string{"one_page", "page_size"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
		probe("fs_smoke.empty", 0, int64(0))
		probe("fs_smoke.heap", 4294967294, int64(0))
		probe("fs_smoke.heap", 0, one)
		probe(nil, -1, nil)
		probe("fs_smoke.heap", 0, one)
		fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT * FROM fs_smoke.pg_freespace(NULL::regclass)", fsNames, fsTypes, nil), b.PG))
		fsBoth(t, p, b, c, fsError(q, "22023", "invalid block number", "fs_smoke.heap", int64(-1)))
		for _, q := range []string{"CREATE ROLE fs_smoke_reader", "GRANT USAGE ON SCHEMA fs_smoke TO fs_smoke_reader", "SET SESSION AUTHORIZATION fs_smoke_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fsBoth(t, p, b, c, fsError(q, "42501", "permission denied for function pg_freespace", "fs_smoke.heap", int64(0)))
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT pg_stat_scan_tables TO fs_smoke_reader", "SET SESSION AUTHORIZATION fs_smoke_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		probe("fs_smoke.heap", 0, one)
		fsBoth(t, p, b, c, fsError("SELECT k FROM fs_smoke.heap", "42501", "permission denied for table heap"))
		fsRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{
			"CREATE VIEW fs_smoke.scalar_view AS SELECT fs_smoke.pg_freespace('fs_smoke.heap'::regclass,0) AS avail",
			"CREATE VIEW fs_smoke.source_view AS SELECT * FROM fs_smoke.pg_freespace('fs_smoke.heap'::regclass)",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		post := func() {
			probe("fs_smoke.heap", 0, one)
			fsBoth(t, p, b, c, fixture.Select("SELECT avail FROM fs_smoke.scalar_view", []string{"avail"}, []string{"INT2"}, [][]any{{one}}))
			fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_smoke.source_view ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), one}}))
			fsDependency(t, p, b, c, "fs_smoke.pg_freespace(regclass,bigint)", "fs_smoke.pg_freespace(regclass)")
			fsBoth(t, p, b, c, fsBodies("fs_smoke"))
		}
		post()
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			retained, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			old, err := retained.QueryContext(p.Ctx, "SELECT * FROM fs_smoke.source_view ORDER BY blkno")
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
				t.Fatal("retained smoke row missing", old.Err())
			}
			var block, avail int64
			if err := old.Scan(&block, &avail); err != nil || block != 0 || avail != 8096 {
				t.Fatal("retained smoke row", block, avail, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained smoke tail", old.Err())
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			if len(metadata) != 2 || metadata[0].DatabaseTypeName() != "INT8" || metadata[1].DatabaseTypeName() != "INT2" {
				t.Fatal("retained smoke metadata", metadata)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			post()
		}
	})
}
