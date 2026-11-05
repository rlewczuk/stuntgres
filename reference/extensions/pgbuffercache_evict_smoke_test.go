package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_evict_smoke_reader"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bceHeap(t, p, b, c, "evict_smoke_heap")
		bceHeap(t, p, b, c, "evict_smoke_other")
		warm := bceWarm(t, p, b, c, "evict_smoke_heap")
		bceClean(t, p, b, c)
		var slot any
		for _, row := range warm {
			if row[4] == int64(0) && row[5] == int64(0) {
				slot = row[0]
			}
		}
		if slot == nil {
			t.Fatal("known heap main block not observed", warm)
		}
		bceExact(t, bceCall(t, p, b, c, false, true, "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", slot), true, false)
		bceCold(t, p, b, c, "evict_smoke_heap")
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.evict_smoke_heap")
		bcUsage(t, p, b, c, "bc_part.evict_smoke_heap", 1)
		bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.evict_smoke_heap"), bcCapacity(t, p, c), 1, 0)
		bceCold(t, p, b, c, "evict_smoke_heap")
		bcBoth(t, p, b, c, bceNullStep("SELECT * FROM bc_part.pg_buffercache_evict(NULL::integer)", true))
		bcBoth(t, p, b, c, bceNullStep("SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", false, nil))
		bcDDL(t, p, b, c, "CREATE ROLE bc_evict_smoke_reader", "GRANT USAGE ON SCHEMA bc_part TO bc_evict_smoke_reader", "SET ROLE bc_evict_smoke_reader")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		bcFailure(t, p, b, c, bceRuntimeDenial("SELECT * FROM bc_part.pg_buffercache_evict_all()", "pg_buffercache_evict_all"), "execute")
		bcDDL(t, p, b, c, "RESET ROLE")
		bceWarm(t, p, b, c, "evict_smoke_heap")
		bceWarm(t, p, b, c, "evict_smoke_other")
		n := bceOccupied(t, p, b, c)
		if b.PG {
			n = 2
		}
		bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()"), bcCapacity(t, p, c), n, 0)
		bceCold(t, p, b, c, "evict_smoke_heap")
		bceCold(t, p, b, c, "evict_smoke_other")
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.evict_smoke_heap")
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.evict_smoke_other")
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bceCold(t, p, b, c, "evict_smoke_heap")
		bceCold(t, p, b, c, "evict_smoke_other")
		bceWarm(t, p, b, c, "evict_smoke_heap")
		bcUsage(t, p, b, c, "bc_part.evict_smoke_heap", 1)
		bceExact(t, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.evict_smoke_heap')"), int64(1), int64(0), int64(0))
		bceCold(t, p, b, c, "evict_smoke_heap")
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.evict_smoke_heap")
		bcUsage(t, p, b, c, "bc_part.evict_smoke_heap", 1)
	})
}
