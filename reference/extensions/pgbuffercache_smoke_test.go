package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This is an actual feature operation/control scenario, not a matrix-test alias.
func TestReferencePgBuffercacheFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_smoke_reader"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE TABLE bc_part.smoke_heap(k integer)",
			"CREATE VIEW bc_part.smoke_wrapper AS SELECT * FROM bc_part.pg_buffercache",
			"CREATE ROLE bc_smoke_reader",
			"GRANT USAGE ON SCHEMA bc_part TO bc_smoke_reader")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.smoke_heap VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.smoke_heap")
		bcUsage(t, p, b, c, "bc_part.smoke_heap", 1)
		bcUnused(t, p, b, c)
		capacity := bcCapacity(t, p, c)
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.smoke_wrapper", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		// Fresh prepared typed invocation has its own caller-controlled names,
		// real occupied target and all nine public descriptor/type assertions.
		names := []string{"id", "node", "space", "database", "fork", "block", "dirty", "usage", "pins"}
		q := "SELECT * FROM " + bcFrom("bc_part.pg_buffercache_pages", names, bcSQLTypes) + " WHERE node=$1::oid AND space=$2::oid AND database=$3::oid"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		node, space, database := bcStorage(t, p, c, "bc_part.smoke_heap")
		rows, err := st.QueryContext(p.Ctx, node, space, database)
		if err != nil {
			t.Fatal(err)
		}
		got := bcRows(t, rows, b.PG, false, names, bcTypes)
		if len(got) == 0 {
			t.Fatal("prepared feature call lost genuine occupied target")
		}
		for _, row := range got {
			for _, cell := range row {
				if cell == nil {
					t.Fatal("occupied smoke cell NULL", row)
				}
			}
			if row[1] != node || row[2] != space || row[3] != database || row[7].(int64) < 0 || row[7].(int64) > 5 || row[8].(int64) < 0 {
				t.Fatal("prepared smoke identity/counters", row)
			}
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_smoke_reader")
		bcFailure(t, p, b, c, bcError("SELECT * FROM bc_part.pg_buffercache", "42501", "permission denied for view pg_buffercache"), "execute")
		bcFailure(t, p, b, c, bcError("SELECT * FROM "+bcFunction(), "42501", "permission denied for function pg_buffercache_pages"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO bc_smoke_reader", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_smoke_reader", "SET SESSION AUTHORIZATION bc_smoke_reader")
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.pg_buffercache", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcFailure(t, p, b, c, bcError("SELECT k FROM bc_part.smoke_heap", "42501", "permission denied for table smoke_heap"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() FROM bc_smoke_reader", "SET SESSION AUTHORIZATION bc_smoke_reader")
		bcFailure(t, p, b, c, bcError("SELECT * FROM bc_part.pg_buffercache", "42501", "permission denied for function pg_buffercache_pages"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_smoke_reader")
		if b.PG {
			return
		}

		// Native controls preserve the installed SQL recipe and ACLs, but never
		// transplant the live read cache or its owning session resources.
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		borrower, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reldatabase=$2::oid", node, database)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		retained := bcRows(t, old, false, true, bcNames, bcTypes)
		if len(retained) != 1 || retained[0][1] != node || retained[0][7] != int64(1) || retained[0][8] != int64(0) {
			t.Fatal("smoke retained snapshot", retained)
		}
		if err := borrower.Close(); err != nil {
			t.Fatal(err)
		}
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bcUsage(t, p, b, c, "bc_part.smoke_heap", 0)
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.smoke_wrapper", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_smoke_reader")
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.pg_buffercache", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.smoke_heap")
		bcUsage(t, p, b, c, "bc_part.smoke_heap", 1)
		bcUnused(t, p, b, c)
	})
}
