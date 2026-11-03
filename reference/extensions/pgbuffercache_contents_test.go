package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheContents(t *testing.T) {
	databases := []string{"bc_contents_other"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.contents_heap(k integer)", "ALTER TABLE bc_part.contents_heap SET(autovacuum_enabled=false)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.contents_heap VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.contents_heap")
		node, space, database := bcStorage(t, p, c, "bc_part.contents_heap")
		if space != 1663 {
			t.Fatal("default placement must be pg_default, not pg_class sentinel", space)
		}
		rows := bcTarget(t, p, b, c, "bc_part.contents_heap")
		if len(rows) == 0 {
			t.Fatal("actual heap read produced no occupancy")
		}
		capacity := bcCapacity(t, p, c)
		var mainBlocks int64 = 1
		if b.PG {
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size('bc_part.contents_heap')/current_setting('block_size')::bigint").Scan(&mainBlocks); err != nil || mainBlocks < 1 {
				t.Fatal("independent physical main-fork bound", mainBlocks, err)
			}
		}
		mainWitness := false
		for _, row := range rows {
			for i, cell := range row {
				if cell == nil {
					t.Fatal("occupied NULL", bcNames[i], row)
				}
			}
			if row[0].(int64) < 1 || row[0].(int64) > capacity || row[1] != node || row[2] != space || row[3] != database {
				t.Fatal("occupied identity", row, node, space, database)
			}
			fork, block, usage, pins := row[4].(int64), row[5].(int64), row[7].(int64), row[8].(int64)
			if fork < 0 || fork > 3 || block < 0 || usage < 0 || usage > 5 || pins < 0 {
				t.Fatal("physical domain", row)
			}
			if fork == 0 {
				mainWitness = true
				if block >= mainBlocks {
					t.Fatal("main block exceeds genuine relation", row, mainBlocks)
				}
			}
		}
		if !mainWitness {
			t.Fatal("read main fork missing", rows)
		}
		bcUsage(t, p, b, c, "bc_part.contents_heap", 1)
		bcUnused(t, p, b, c)
		// Check the complete source too: no partially NULL unused records, and
		// no made-up default database or tablespace for occupied records.
		all := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache", true)
		for _, row := range all {
			if row[1] == nil {
				for i := 1; i < 9; i++ {
					if row[i] != nil {
						t.Fatal("partial unused record", row)
					}
				}
				continue
			}
			for _, cell := range row {
				if cell == nil {
					t.Fatal("partial occupied record", row)
				}
			}
			if row[2].(int64) <= 0 || row[3].(int64) < 0 {
				t.Fatal("invalid occupied catalog address", row)
			}
			if row[3] == int64(0) && row[2] != int64(1664) {
				t.Fatal("shared storage not in pg_global", row)
			}
		}
		bcDDL(t, p, b, c, "CREATE DATABASE bc_contents_other")
		pool, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, "bc_contents_other", "")
		other, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		ob := b
		ob.DB, ob.Recorder = pool, rec
		bcDDL(t, p, ob, other, "CREATE TABLE public.bc_other_heap(k integer)")
		bcRun(t, p, ob, other, fixture.Exec("INSERT INTO public.bc_other_heap VALUES(1)", 1))
		bcReadHeap(t, p, ob, other, "SELECT k FROM public.bc_other_heap")
		otherNode, otherSpace, otherDatabase := bcStorage(t, p, other, "public.bc_other_heap")
		if database == otherDatabase {
			t.Fatal("different databases share catalog identity", database)
		}
		foreignRows := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, otherNode, otherSpace, otherDatabase)
		if len(foreignRows) == 0 || (!b.PG && len(foreignRows) != 1) {
			t.Fatal("cluster cache omitted genuine other-database read", foreignRows)
		}
		for _, row := range foreignRows {
			if row[1] != otherNode || row[2] != otherSpace || row[3] != otherDatabase {
				t.Fatal("foreign database identity confused with local pg_class", row)
			}
		}
		bcUsage(t, p, b, c, "bc_part.contents_heap", 1)
	})
}
