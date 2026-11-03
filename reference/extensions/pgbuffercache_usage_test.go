package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheReadUsage(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE TABLE bc_part.usage_heap(k integer)",
			"CREATE TABLE bc_part.other_heap(k integer)",
			"CREATE TABLE bc_part.empty_heap(k integer)",
			"ALTER TABLE bc_part.usage_heap SET(autovacuum_enabled=false)",
			"CREATE VIEW bc_part.usage_view AS SELECT k FROM bc_part.usage_heap")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.usage_heap VALUES(1)", 1))
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.other_heap VALUES(1)", 1))
		q := "SELECT k FROM bc_part.usage_heap"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		// PostgreSQL INSERT already touches real buffers. The native read-only
		// approximation deliberately starts cold; this is not SQL ACL leniency.
		if !b.PG {
			bcUsage(t, p, b, c, "bc_part.usage_heap", 0)
		}
		for _, query := range []string{"SELECT k FROM bc_part.usage_heap LIMIT 0", "SELECT k FROM bc_part.usage_heap WHERE false", "SELECT k FROM bc_part.empty_heap"} {
			bcBoth(t, p, b, c, fixture.Select(query, []string{"k"}, []string{"INT4"}, nil))
		}
		if !b.PG {
			bcUsage(t, p, b, c, "bc_part.usage_heap", 0)
			bcUsage(t, p, b, c, "bc_part.empty_heap", 0)
		}
		bcReadHeap(t, p, b, c, q)
		bcUsage(t, p, b, c, "bc_part.usage_heap", 1)
		// Observing the provider, describing it, and scanning unrelated storage
		// must not count as another invocation of this heap's source.
		for range 2 {
			bcQuery(t, p, b, c, "SELECT * FROM "+bcFunction()+" WHERE false", false)
			bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE bufferid=1", true)
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.other_heap")
			bcUsage(t, p, b, c, "bc_part.usage_heap", 1)
		}
		bcReadHeap(t, p, b, held, q)
		bcUsage(t, p, b, c, "bc_part.usage_heap", 2)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.usage_view")
		bcUsage(t, p, b, c, "bc_part.usage_heap", 3)
		// A real nonempty source is reached even though its filter emits nothing.
		bcRun(t, p, b, c, fixture.Select("SELECT k FROM bc_part.usage_heap WHERE k=99", []string{"k"}, []string{"INT4"}, nil))
		bcUsage(t, p, b, c, "bc_part.usage_heap", 4)
		bcDDL(t, p, b, c, "BEGIN")
		bcReadHeap(t, p, b, held, q)
		bcDDL(t, p, b, c, "ROLLBACK")
		bcUsage(t, p, b, c, "bc_part.usage_heap", 5)
		for range 3 {
			bcReadHeap(t, p, b, held, q)
		}
		bcUsage(t, p, b, c, "bc_part.usage_heap", 5)
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.alias_heap(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.alias_heap VALUES(1)", 1))
		aliasQuery := "SELECT a.k FROM bc_part.alias_heap a CROSS JOIN bc_part.alias_heap b"
		bcReadHeap(t, p, b, c, aliasQuery)
		bcUsage(t, p, b, c, "bc_part.alias_heap", 2)
		bcRun(t, p, b, c, fixture.Prepared(fixture.Select(aliasQuery, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})))
		bcUsage(t, p, b, c, "bc_part.alias_heap", 4)

		bcDDL(t, p, b, c, "CREATE TEMP TABLE bc_temp(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_temp VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_temp")
		if rows := bcTarget(t, p, b, c, "bc_temp"); len(rows) != 0 {
			t.Fatal("TEMP leaked into shared cache", rows)
		}
		bcDDL(t, p, b, c, "DROP TABLE bc_temp")

		// Private storage incarnations may be visible to their own transaction,
		// but the native shared logical observer cannot publish private identity.
		bcDDL(t, p, b, c, "BEGIN", "CREATE TABLE bc_part.private_heap(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.private_heap VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.private_heap")
		node, space, database := bcStorage(t, p, c, "bc_part.private_heap")
		if !b.PG {
			if rows := bcTarget(t, p, b, c, "bc_part.private_heap"); len(rows) != 0 {
				t.Fatal("private incarnation globally occupied", rows)
			}
		}
		bcDDL(t, p, b, c, "ROLLBACK")
		if !b.PG {
			rows := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, node, space, database)
			if len(rows) != 0 {
				t.Fatal("rolled-back storage remained occupied", rows)
			}
		}
		t.Run("sampling_read_frontier", func(t *testing.T) {
			bcSamplingReadUsage(t, p, b, c)
		})
	})
}

func bcSamplingReadUsage(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	bcDDL(t, p, b, c, "CREATE EXTENSION tsm_system_rows SCHEMA bc_part")
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION tsm_system_rows CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	for _, route := range []string{"direct", "prepared"} {
		t.Run(route, func(t *testing.T) {
			for _, kind := range []string{"heap", "hierarchy"} {
				t.Run(kind, func(t *testing.T) {
					table := "bc_part.sample_" + route + "_" + kind
					tables := []string{table}
					bcDDL(t, p, b, c, "CREATE TABLE "+table+"(k integer)")
					bcRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					want := [][]any{{int64(1)}}
					if kind == "hierarchy" {
						child := table + "_child"
						bcDDL(t, p, b, c, "CREATE TABLE "+child+"() INHERITS("+table+")")
						bcRun(t, p, b, c, fixture.Exec("INSERT INTO "+child+" VALUES(1)", 1))
						tables = append(tables, child)
						// Released inheritance sampling applies the tuple request
						// to each physical RTE. One row per heap is deterministic.
						want = append(want, []any{int64(1)})
					}
					query := "SELECT k FROM " + table + " TABLESAMPLE bc_part.system_rows($1) ORDER BY k"
					var source fixture.Executor = c
					if route == "prepared" {
						statement, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						source = fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
					}
					observe := func(usage int64) {
						t.Helper()
						// PG asserts independently specified sampling SQL rows;
						// INSERT already touches its physical shared buffers.
						// Only the native logical model promises exact usage/pins.
						if !b.PG {
							for _, table := range tables {
								bcUsage(t, p, b, c, table, usage)
							}
						}
					}
					sample := func(count int64, rows [][]any) {
						t.Helper()
						bcRun(t, p, b, source, fixture.Select(query, []string{"k"}, []string{"INT4"}, rows, count))
					}
					observe(0) // Preparation and INSERT are not native reads.
					sample(0, nil)
					observe(0) // A cold nonempty heap remains absent.
					sample(1, want)
					observe(1)
					sample(0, nil)
					observe(1) // Warm zero sampling changes neither usage nor pins.
					sample(0, nil)
					observe(1)
					sample(1, want)
					observe(2) // A fresh direct/held invocation is a new read.
				})
			}
		})
	}
}
