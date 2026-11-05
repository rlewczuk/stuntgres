package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMALifecycle(t *testing.T) {
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.lifecycle_kept(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.lifecycle_kept VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
		capacity := bcCapacity(t, p, c)
		queries := []string{"SELECT * FROM bc_part.pg_buffercache_numa", "SELECT * FROM " + bcNUMAFunction()}
		held := make([]fixture.HeldStatement, len(queries))
		for i, q := range queries {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, source := range []fixture.Executor{c, held[i], held[i]} {
				if b.PG && bcNUMAPGUnavailable {
					bcNUMADemand(t, p, b, source, fixture.Select(q, bcNUMANames, bcNUMATypes, nil))
					continue
				}
				rows, err := source.QueryContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				bcMetadata(t, rows, b.PG, i == 0, bcNUMANames, bcNUMATypes)
				if !rows.Next() { rows.Close(); t.Fatal("NUMA early-close source empty", rows.Err()) }
				var id, page int64
				var node sql.NullInt64
				if err := rows.Scan(&id, &page, &node); err != nil { rows.Close(); t.Fatal(err) }
				if id < 1 || id > capacity || page < 0 || (node.Valid && node.Int64 < 0) { rows.Close(); t.Fatal("early row domain", id, page, node) }
				if err := rows.Close(); err != nil { t.Fatal(err) }
			}
			// Exhaust the same held source after early close or exact physical
			// failure, then use it again; neither path retains a poisoned cursor.
			bcNUMARead(t, p, b, held[i], q, i == 0, capacity)
			bcNUMARead(t, p, b, held[i], q, i == 0, capacity)
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		bcDDL(t, p, b, c, "DROP EXTENSION pg_buffercache CASCADE")
		for i, q := range queries {
			s := bcError(q, "42P01", "relation \"bc_part.pg_buffercache_numa\" does not exist")
			if i == 1 {
				s = bcError(q, "42883", "function bc_part.pg_buffercache_numa_pages() does not exist")
				s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			}
			s.Diagnostic.Position = fixture.Position(15)
			bcFailure(t, p, b, c, s, "prepare")
			s.ErrorStage = "execute"
			bcRun(t, p, b, held[i], s)
		}
		bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		for i, q := range queries { bcNUMARead(t, p, b, held[i], q, i == 0, capacity) }
		if b.PG { return }

		t.Run("native_cancellation_and_no_pins", func(t *testing.T) {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for i, q := range queries {
				rows, err := c.QueryContext(ctx, q)
				if rows != nil { rows.Close(); t.Fatal("pre-canceled Query exposed rows") }
				if !errors.Is(err, context.Canceled) { t.Fatal("ordinary Query cancellation", err) }
				st, err := c.PrepareContext(ctx, q)
				if st != nil { st.Close(); t.Fatal("pre-canceled Prepare exposed statement") }
				if !errors.Is(err, context.Canceled) { t.Fatal("ordinary Prepare cancellation", err) }
				rows, err = held[i].Statement.QueryContext(ctx)
				if rows != nil { rows.Close(); t.Fatal("pre-canceled held Execute exposed rows") }
				if !errors.Is(err, context.Canceled) { t.Fatal("ordinary held cancellation", err) }
				bcNUMARead(t, p, b, held[i], q, i == 0, capacity)
			}
			// A genuinely open heap source owns one pin. NUMA observation must
			// neither release that pin nor acquire one of its own, including at
			// a deterministically admitted source-work cancellation boundary.
			stopHeap := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", false)
			bcPins(t, p, b, c, 1)
			before := bcTarget(t, p, b, c, "bc_part.lifecycle_kept")
			for _, prepared := range []bool{false, true} {
				stop := bcPausedQuery(t, p, b, "SELECT bufferid FROM "+bcNUMAFunction()+" WHERE bufferid::text LIKE '1'", prepared)
				bcNUMAUnchanged(t, before, bcTarget(t, p, b, c, "bc_part.lifecycle_kept"))
				if err := stop(); !errors.Is(err, context.Canceled) { t.Fatal("admitted NUMA source cancellation", err) }
				bcNUMAUnchanged(t, before, bcTarget(t, p, b, c, "bc_part.lifecycle_kept"))
			}
			if err := stopHeap(); !errors.Is(err, context.Canceled) { t.Fatal("heap owner cancellation", err) }
			bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 2)
			for i, q := range queries { bcNUMARead(t, p, b, held[i], q, i == 0, capacity) }
			bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 2)
		})
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		for i, q := range queries { bcNUMARead(t, p, b, held[i], q, i == 0, capacity) }
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 0)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
	})
}
