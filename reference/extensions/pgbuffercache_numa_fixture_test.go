package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var bcNUMANames = []string{"bufferid", "os_page_num", "numa_node"}
var bcNUMATypes = []string{"INT4", "INT8", "INT4"}
var bcNUMASQLTypes = []string{"integer", "bigint", "integer"}

// The pinned PostgreSQL 18.6 container reports initialization unavailable, as
// observed in both oracle groups. REL_18_6 checks initialization before the
// result descriptor; every demanded call must return this fixed diagnostic.
const bcNUMAPGUnavailable = true
const bcNUMAUnavailableMessage = "libnuma initialization failed or NUMA is not supported on this platform"

func bcNUMAFunction() string {
	return bcFrom("bc_part.pg_buffercache_numa_pages", bcNUMANames, bcNUMASQLTypes)
}

func bcNUMAScenario(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if install {
			bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		}
		body(t, p, b, c)
	})
}

func bcNUMADemandStep(b fixture.Backend, s fixture.Step) fixture.Step {
	if b.PG && bcNUMAPGUnavailable {
		failure := bcError(s.Query, "XX000", bcNUMAUnavailableMessage)
		failure.Args, failure.Prepared, failure.ErrorStage = s.Args, s.Prepared, "execute"
		return failure
	}
	return s
}

func bcNUMADemand(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	bcRun(t, p, b, c, bcNUMADemandStep(b, s))
}

func bcNUMABoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	bcBoth(t, p, b, c, bcNUMADemandStep(b, s))
}

func bcNUMACount(query string, capacity int64) fixture.Step {
	return fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{capacity}})
}

func bcNUMAEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, from string, view bool) {
	t.Helper()
	for _, suffix := range []string{" WHERE false", " LIMIT 0"} {
		s := fixture.Select("SELECT * FROM "+from+suffix, bcNUMANames, bcNUMATypes, nil)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			bcMetadata(t, rows, b.PG, view, bcNUMANames, bcNUMATypes)
		}
		bcBoth(t, p, b, c, s)
	}
}

// The native bag is independently specified, not learned from pg_buffercache or
// a second execution. PostgreSQL's page multiplicity is deliberately not golden.
func bcNUMATopology(t *testing.T, rows [][]any, pg bool, capacity int64) {
	t.Helper()
	seen := make(map[int64]int, int(capacity))
	zero := false
	for _, row := range rows {
		id, idOK := row[0].(int64)
		page, pageOK := row[1].(int64)
		if !idOK || !pageOK || id < 1 || id > capacity || page < 0 {
			t.Fatal("NUMA non-NULL identity/page domain", row, capacity)
		}
		if row[2] != nil {
			node, ok := row[2].(int64)
			if !ok || node < 0 {
				t.Fatal("NUMA node must be nonnegative or SQL NULL, never -1", row)
			}
		}
		if !pg && (page != id-1 || row[2] != int64(0)) {
			t.Fatal("virtual supported single-node topology", row)
		}
		seen[id]++
		zero = zero || page == 0
	}
	if int64(len(seen)) != capacity || !zero {
		t.Fatal("NUMA omitted capacity IDs or relative page zero", len(seen), capacity, zero)
	}
	for id := int64(1); id <= capacity; id++ {
		if seen[id] < 1 || (!pg && seen[id] != 1) {
			t.Fatal("NUMA bag multiplicity", id, seen[id])
		}
	}
}

func bcNUMARead(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, view bool, capacity int64) [][]any {
	t.Helper()
	if b.PG && bcNUMAPGUnavailable {
		bcNUMADemand(t, p, b, c, fixture.Select(query, bcNUMANames, bcNUMATypes, nil))
		return nil
	}
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	got := bcRows(t, rows, b.PG, view, bcNUMANames, bcNUMATypes)
	bcNUMATopology(t, got, b.PG, capacity)
	return got
}

func bcNUMAUnchanged(t *testing.T, before, after [][]any) {
	t.Helper()
	// A canonical ORDER BY bufferid is supplied by callers: counters and pins
	// must be unchanged, not merely valid or equal after discarding duplicates.
	if !reflect.DeepEqual(before, after) {
		t.Fatal("NUMA observation changed logical cache occupancy/usage/pins", before, after)
	}
}

func bcNUMACatalogLinks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, function, view string) []int64 {
	t.Helper()
	ids := make([]int64, 4)
	q := "SELECT p.oid::bigint,c.oid::bigint,c.reltype::bigint,t.typarray::bigint FROM pg_proc p CROSS JOIN pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE p.oid=$1::regprocedure AND c.oid=$2::regclass"
	if err := c.QueryRowContext(p.Ctx, q, function, view).Scan(&ids[0], &ids[1], &ids[2], &ids[3]); err != nil {
		t.Fatal("NUMA resident catalog identities", err)
	}
	for _, id := range ids {
		if id <= 0 {
			t.Fatal("missing NUMA resident identity", ids)
		}
	}
	if ids[2] == ids[3] {
		t.Fatal("row/array identity alias", ids)
	}
	bcBoth(t, p, b, c, fixture.Select("SELECT c.relkind::text AS kind,t.typtype::text AS type,t.typrelid::bigint AS relation,a.typelem::bigint AS element FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=$1::oid", []string{"kind", "type", "relation", "element"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, [][]any{{"v", "c", ids[1], ids[2]}}, ids[1]))
	return ids
}

func bcNUMAMembership(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, ids []int64, n int64) {
	t.Helper()
	q := "SELECT count(*) AS n FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e' AND ((classid=1255 AND objid=$1::oid) OR (classid=1259 AND objid=$2::oid))"
	bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, ids[0], ids[1]))
}
