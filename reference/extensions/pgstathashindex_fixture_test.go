package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgstathashindexNames = []string{"version", "bucket_pages", "overflow_pages", "bitmap_pages", "unused_pages", "live_items", "dead_items", "free_percent"}
var pgstathashindexTypes = []string{"INT4", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8"}

func pgstathashindexCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ph_part")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgstattuple CASCADE", "DROP SCHEMA IF EXISTS ph_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA ph_part", "CREATE EXTENSION pgstattuple SCHEMA ph_part", "CREATE TABLE ph_part.ph_heap(k integer)", "CREATE INDEX ph_index ON ph_part.ph_heap USING hash(k)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func pgstathashindexBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func pgstathashindexNull(query string, args ...any) fixture.Step {
	return fixture.Select(query, pgstathashindexNames, pgstathashindexTypes, [][]any{make([]any, 8)}, args...)
}

// Raw columns are checked before scanning. Fresh build counts are independent of
// heap visibility hints; PostgreSQL physical pages are not native model goldens.
// Negative expected counts select private-phase shape/model assertions without
// imposing a transaction-visible membership trajectory on the root sample.
func pgstathashindexStats(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, live, dead int64, args ...any) {
	t.Helper()
	check := func(rows *sql.Rows, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, pgstathashindexNames) {
			t.Fatalf("columns %v: %v", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			if typ.DatabaseTypeName() != pgstathashindexTypes[i] {
				t.Fatalf("%s type %s", names[i], typ.DatabaseTypeName())
			}
		}
		if !rows.Next() {
			t.Fatal("missing statistics row", rows.Err())
		}
		var version, buckets, overflow, bitmap, unused, gotLive, gotDead int64
		var free float64
		if err := rows.Scan(&version, &buckets, &overflow, &bitmap, &unused, &gotLive, &gotDead, &free); err != nil {
			t.Fatal(err)
		}
		if version != 4 || buckets < 2 || overflow < 0 || bitmap < 1 || unused < 0 || gotLive < 0 || gotDead < 0 || live >= 0 && gotLive != live || dead >= 0 && gotDead != dead || math.IsNaN(free) || math.IsInf(free, 0) || free < 0 || free > 100 {
			t.Fatalf("statistics got (%d,%d,%d,%d,%d,%d,%d,%g), want live/dead %d/%d and released hash invariants", version, buckets, overflow, bitmap, unused, gotLive, gotDead, free, live, dead)
		}
		if gotLive+gotDead == 0 && free != 100 {
			t.Fatalf("empty free_percent %g, want 100", free)
		}
		if !b.PG {
			// Independent fixed compact model: 407 items/page, four base buckets,
			// one bitmap, no unused pages, 20 bytes/item and 8152 usable/page.
			if gotDead != 0 { t.Fatal("native simulated dead items", gotDead) }
			n := gotLive
			pages := n / 407
			if n%407 != 0 {
				pages++
			}
			if pages < 4 {
				pages = 4
			}
			wantFree := 100 * float64(8152*pages-20*n) / float64(8152*pages)
			if buckets != 4 || overflow != pages-4 || bitmap != 1 || unused != 0 || free != wantFree {
				t.Fatalf("native compact layout (%d,%d,%d,%d,%g), want (4,%d,1,0,%g)", buckets, overflow, bitmap, unused, free, pages-4, wantFree)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("statistics tail", rows.Err())
		}
	}
	check(c.QueryContext(p.Ctx, query, args...))
	st, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check(st.QueryContext(p.Ctx, args...))
}

func pgstathashindexCounts(query string, live, dead int64, args ...any) fixture.Step {
	return fixture.Select(query, []string{"live_items", "dead_items"}, []string{"INT8", "INT8"}, [][]any{{live, dead}}, args...)
}

func pgstathashindexSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("INSERT INTO ph_part.ph_heap VALUES(1),(1),(2),(NULL),(-1)", 5),
		fixture.Exec("REINDEX INDEX ph_part.ph_index", 0),
		fixture.Exec("CREATE INDEX ph_sibling ON ph_part.ph_heap USING hash(k)", 0),
		fixture.Exec("CREATE INDEX ph_expression ON ph_part.ph_heap USING hash(coalesce(k,0))", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
