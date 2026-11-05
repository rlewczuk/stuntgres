package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMAFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_numa_smoke_reader"})
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.numa_smoke_heap(k integer)", "CREATE ROLE bc_numa_smoke_reader", "GRANT USAGE ON SCHEMA bc_part TO bc_numa_smoke_reader", "CREATE VIEW bc_part.numa_smoke_wrapper AS SELECT * FROM bc_part.pg_buffercache_numa")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.numa_smoke_heap VALUES(1)", 1))
		capacity := bcCapacity(t, p, c)
		viewQ := "SELECT * FROM bc_part.pg_buffercache_numa WHERE bufferid=1"
		names := []string{"id", "page", "node"}
		functionQ := "SELECT * FROM " + bcFrom("bc_part.pg_buffercache_numa_pages", names, bcNUMASQLTypes) + " WHERE id=$1::integer"
		st, err := c.PrepareContext(p.Ctx, functionQ)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: functionQ}
		boundary := func(source fixture.Executor, q string, view bool, id int64, args ...any) [][]any {
			t.Helper()
			fields := names
			if view { fields = bcNUMANames }
			if b.PG && bcNUMAPGUnavailable {
				s := fixture.Select(q, fields, bcNUMATypes, nil, args...)
				bcNUMADemand(t, p, b, source, s)
				return nil
			}
			rows, err := source.QueryContext(p.Ctx, q, args...)
			if err != nil { t.Fatal(err) }
			got := bcRows(t, rows, b.PG, view, fields, bcNUMATypes)
			if len(got) == 0 { t.Fatal("NUMA boundary omitted genuine buffer", id) }
			zero := false
			for _, row := range got {
				if row[0] != id || row[1] == nil || row[1].(int64) < 0 || (row[2] != nil && row[2].(int64) < 0) { t.Fatal("physical boundary domain", id, row) }
				zero = zero || row[1] == int64(0)
			}
			if b.PG {
				if id == 1 && !zero { t.Fatal("first relative OS page absent", got) }
			} else if !reflect.DeepEqual(got, [][]any{{id, id-1, int64(0)}}) {
				t.Fatal("native boundary exact value", got, id)
			}
			return got
		}
		boundary(c, viewQ, true, 1)
		boundary(held, functionQ, false, 1, int64(1))
		boundary(held, functionQ, false, capacity, capacity)
		fresh, err := c.PrepareContext(p.Ctx, functionQ)
		if err != nil { t.Fatal(err) }
		boundary(fixture.HeldStatement{Executor: c, Statement: fresh, SQL: functionQ}, functionQ, false, 1, int64(1))
		if err := fresh.Close(); err != nil { t.Fatal(err) }
		bcNUMAEmpty(t, p, b, c, "bc_part.pg_buffercache_numa", true)
		bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_smoke_heap")
		occupied := bcTarget(t, p, b, c, "bc_part.numa_smoke_heap")
		if len(occupied) == 0 { t.Fatal("smoke heap must occupy genuine buffers") }
		node, space, database := bcStorage(t, p, c, "bc_part.numa_smoke_heap")
		join := "SELECT count(DISTINCT n.bufferid) AS n FROM bc_part.pg_buffercache_numa n JOIN bc_part.pg_buffercache c ON c.bufferid=n.bufferid WHERE c.relfilenode=$1::oid AND c.reltablespace=$2::oid AND c.reldatabase=$3::oid"
		s := bcNUMACount(join, int64(len(occupied)))
		s.Args = []any{node, space, database}
		bcNUMABoth(t, p, b, c, s)
		boundary(c, viewQ, true, 1)
		if !b.PG { bcNUMAUnchanged(t, occupied, bcTarget(t, p, b, c, "bc_part.numa_smoke_heap")) }
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		bcFailure(t, p, b, c, bcError(viewQ, "42501", "permission denied for view pg_buffercache_numa"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_smoke_reader", "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
		bcFailure(t, p, b, c, bcError(viewQ, "42501", "permission denied for function pg_buffercache_numa_pages"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO bc_numa_smoke_reader", "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
		boundary(c, viewQ, true, 1)
		boundary(held, functionQ, false, 1, int64(1))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() FROM bc_numa_smoke_reader", "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
		denied := bcError(functionQ, "42501", "permission denied for function pg_buffercache_numa_pages")
		denied.Args = []any{int64(1)}
		bcRun(t, p, b, held, denied)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO bc_numa_smoke_reader", "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
		boundary(held, functionQ, false, capacity, capacity)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		bcNUMACatalogLinks(t, p, b, c, "bc_part.pg_buffercache_numa_pages()", "bc_part.pg_buffercache_numa")
		if b.PG { return }

		t.Run("native_smoke_control_roundtrip", func(t *testing.T) {
			old := boundary(c, viewQ, true, 1)
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			borrower, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer borrower.Close()
			retainedRows, err := borrower.QueryContext(p.Ctx, viewQ)
			if err != nil { t.Fatal(err) }
			defer retainedRows.Close()
			metadata, err := retainedRows.ColumnTypes()
			if err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			retained := bcRows(t, retainedRows, false, true, bcNUMANames, bcNUMATypes)
			if !reflect.DeepEqual(retained, old) { t.Fatal("retained smoke rows changed across Reset", retained, old) }
			for i, col := range metadata {
				if col.Name() != bcNUMANames[i] || col.DatabaseTypeName() != bcNUMATypes[i] { t.Fatal("retained smoke metadata mutated", i, col) }
			}
			if err := borrower.Close(); err != nil { t.Fatal(err) }
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", 0))
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_class WHERE relname='numa_smoke_heap' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='bc_part')", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			bcNUMACatalogLinks(t, p, b, c, "bc_part.pg_buffercache_numa_pages()", "bc_part.pg_buffercache_numa")
			bcUsage(t, p, b, c, "bc_part.numa_smoke_heap", 0)
			bcNUMARead(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_numa", true, capacity)
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(DISTINCT bufferid) AS n FROM bc_part.numa_smoke_wrapper", capacity))
			bcUsage(t, p, b, c, "bc_part.numa_smoke_heap", 0)
			if !reflect.DeepEqual(old, [][]any{{int64(1), int64(0), int64(0)}}) { t.Fatal("retained smoke row mutated", old) }
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.oid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>p.proacl", 1))
			bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_numa_smoke_reader")
			boundary(held, functionQ, false, 1, int64(1))
			boundary(c, viewQ, true, 1)
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_smoke_heap")
			bcUsage(t, p, b, c, "bc_part.numa_smoke_heap", 1)
			node, space, database := bcStorage(t, p, c, "bc_part.numa_smoke_heap")
			s := bcNUMACount(join, 1)
			s.Args = []any{node, space, database}
			bcBoth(t, p, b, c, s)
			bcUsage(t, p, b, c, "bc_part.numa_smoke_heap", 1)
		})
	})
}
