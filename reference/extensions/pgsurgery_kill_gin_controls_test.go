package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type ksGinSurvivor struct {
	k        int64
	v        string
	a, terms any
	pending  bool
}

const ksGinTerms = "'cat':1 'rat':2"

// Placement expectations describe genuine accepted writes, not PostgreSQL page
// counts. Repaired versions missing from the source index rebuild in main.
func TestReferencePgsurgeryKillGinControls(t *testing.T) {
	ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION amcheck SCHEMA ks_part", 0))
		defer c.ExecContext(p.Ctx, "DROP EXTENSION amcheck CASCADE")
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(route, func(t *testing.T) {
				for _, name := range []string{"pending_prefix", "mixed_main_pending", "killed_pending_before_main", "repaired_additional"} {
					t.Run(name, func(t *testing.T) {
						run := func(s fixture.Step) { freezeRun(t, p, b, c, s) }
						run(fixture.Exec("CREATE TABLE ks_part.gin_heap(k integer,v text,a integer[],terms tsvector)", 0))
						defer c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS ks_part.gin_heap CASCADE")
						run(fixture.Exec("ALTER TABLE ks_part.gin_heap SET(autovacuum_enabled=false)", 0))
						run(fixture.Exec("CREATE INDEX kill_gin ON ks_part.gin_heap USING gin(a,terms)", 0))
						apply := func(query string, tids ...string) {
							s := ksVoid(query, "ks_part.gin_heap", freezeTIDs(tids...))
							s.Prepared = prepared
							run(s)
						}
						var want []ksGinSurvivor
						switch name {
						case "pending_prefix":
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(1,'killed','{2,2}','cat:1 rat:2'),(2,'pending','{2,2}','cat:1 rat:2')", 2))
							h := ksGinCapture(t, p, c)
							ksGinCapturedPayloads(t, h, []freezeHeader{{k: 1, v: "killed"}, {k: 2, v: "pending"}})
							apply(killSQL, h[0].tid)
							want = []ksGinSurvivor{{2, "pending", "{2,2}", ksGinTerms, true}}
						case "mixed_main_pending":
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=off)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(1,'killed_main_before','{2,2}','cat:1 rat:2'),(2,'main_null',NULL,NULL)", 2))
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=on)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(3,'pending_empty','{}','')", 1))
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=off)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(4,'killed_main_between','{2,2}','cat:1 rat:2'),(5,'main','{2,2}','cat:1 rat:2')", 2))
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=on)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(6,'killed_pending','{2,2}','cat:1 rat:2'),(7,'pending','{2,2}','cat:1 rat:2')", 2))
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=off)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(8,'killed_main_after','{2,2}','cat:1 rat:2')", 1))
							h := ksGinCapture(t, p, c)
							ksGinCapturedPayloads(t, h, []freezeHeader{{k: 1, v: "killed_main_before"}, {k: 2, v: "main_null"}, {k: 3, v: "pending_empty"}, {k: 4, v: "killed_main_between"}, {k: 5, v: "main"}, {k: 6, v: "killed_pending"}, {k: 7, v: "pending"}, {k: 8, v: "killed_main_after"}})
							apply(killSQL, h[0].tid, h[3].tid, h[5].tid, h[7].tid)
							want = []ksGinSurvivor{{2, "main_null", nil, nil, false}, {3, "pending_empty", "{}", "", true}, {5, "main", "{2,2}", ksGinTerms, false}, {7, "pending", "{2,2}", ksGinTerms, true}}
						case "killed_pending_before_main":
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(1,'killed_pending','{2,2}','cat:1 rat:2')", 1))
							run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=off)", 0))
							run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(2,'main','{2,2}','cat:1 rat:2')", 1))
							h := ksGinCapture(t, p, c)
							ksGinCapturedPayloads(t, h, []freezeHeader{{k: 1, v: "killed_pending"}, {k: 2, v: "main"}})
							apply(killSQL, h[0].tid)
							// The old ordinal 1 exists in replay, but names the wrong
							// version. Restore success alone cannot detect this defect.
							want = []ksGinSurvivor{{2, "main", "{2,2}", ksGinTerms, false}}
						case "repaired_additional":
							want = ksGinRepairedSeed(t, p, b, c, apply)
						}
						ksGinState(t, p, b, c, want)
						if b.PG {
							// SQL rows, predicate results and valid index metadata are
							// shared. Native snapshot placement has no server API peer;
							// physical pending counts are deliberately not parity data.
							return
						}
						ksGinControls(t, p, b, c, want)
					})
				}
			})
		}
	})
}

func ksGinCapture(t *testing.T, p *fixture.Pair, c *sql.Conn) []freezeHeader {
	t.Helper()
	return freezeCapture(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM ks_part.gin_heap ORDER BY k,v")
}

func ksGinCapturedPayloads(t *testing.T, got, want []freezeHeader) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("captured %d GIN rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].k != want[i].k || got[i].v != want[i].v {
			t.Fatalf("captured GIN payload %d = (%d,%q), want (%d,%q)", i, got[i].k, got[i].v, want[i].k, want[i].v)
		}
	}
}

func ksGinRepairedSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, apply func(string, ...string)) []ksGinSurvivor {
	t.Helper()
	run := func(s fixture.Step) { freezeRun(t, p, b, c, s) }
	run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=off)", 0))
	// Establish a real pruning horizon before creating any dead predecessor.
	horizon := freezeConn(t, p, b)
	defer horizon.Close()
	defer horizon.ExecContext(p.Ctx, "ROLLBACK")
	freezeRun(t, p, b, horizon, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	freezeRun(t, p, b, horizon, fixture.Select("SELECT count(*) AS n FROM ks_part.gin_heap", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(10,'retained','{2,2}','cat:1 rat:2'),(20,'killed_main','{2,2}','cat:1 rat:2')", 2))
	h := ksGinCapture(t, p, c)
	ksGinCapturedPayloads(t, h, []freezeHeader{{k: 10, v: "retained"}, {k: 20, v: "killed_main"}})
	successor := freezeOne(t, p, c, "UPDATE ks_part.gin_heap SET v='successor',a='{8}',terms='dog:1' WHERE k=10 RETURNING k,v,ctid,xmin,xmax")
	if successor.k != 10 || successor.v != "successor" || successor.tid == h[0].tid {
		t.Fatal("retained fixture did not create a distinct genuine successor", successor)
	}
	const repairSQL = "SELECT ks_part.heap_force_freeze($1::regclass,$2::tid[]) AS killed"
	apply(repairSQL, h[0].tid)
	run(fixture.Exec("BEGIN", 0))
	defer c.ExecContext(p.Ctx, "ROLLBACK")
	aborted := freezeOne(t, p, c, "INSERT INTO ks_part.gin_heap VALUES(30,'aborted','{2,2}','cat:1 rat:2') RETURNING k,v,ctid,xmin,xmax")
	if aborted.k != 30 || aborted.v != "aborted" {
		t.Fatal("aborted fixture payload", aborted)
	}
	run(fixture.Exec("ROLLBACK", 0))
	// No intervening scan/pruning: repair the real discarded physical version.
	apply(repairSQL, aborted.tid)
	// Repair has made both real versions independently live. Release the
	// horizon's relation/index read locks before changing index options.
	freezeRun(t, p, b, horizon, fixture.Exec("ROLLBACK", 0))
	run(fixture.Exec("ALTER INDEX ks_part.kill_gin SET(fastupdate=on)", 0))
	run(fixture.Exec("INSERT INTO ks_part.gin_heap VALUES(40,'killed_pending','{2,2}','cat:1 rat:2'),(50,'pending_repaired','{2,2}','cat:1 rat:2')", 2))
	pending := freezeCapture(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM ks_part.gin_heap WHERE k>=40 ORDER BY k,v")
	ksGinCapturedPayloads(t, pending, []freezeHeader{{k: 40, v: "killed_pending"}, {k: 50, v: "pending_repaired"}})
	// This genuine pending member is in both the head vector and Additional;
	// export must deduplicate its exact VersionID, not its shared logical RowID.
	apply(repairSQL, pending[1].tid)
	apply(killSQL, h[1].tid, pending[0].tid)
	return []ksGinSurvivor{{10, "retained", "{2,2}", ksGinTerms, false}, {10, "successor", "{8}", "'dog':1", false}, {30, "aborted", "{2,2}", ksGinTerms, false}, {50, "pending_repaired", "{2,2}", ksGinTerms, true}}
}

func ksGinState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want []ksGinSurvivor) {
	t.Helper()
	var rows, matches [][]any
	for _, row := range want {
		rows = append(rows, []any{row.k, row.v, row.a, row.terms})
		if row.a == "{2,2}" {
			matches = append(matches, []any{row.k, row.v})
		}
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,a::text AS a,terms::text AS terms FROM ks_part.gin_heap ORDER BY k,v", []string{"k", "v", "a", "terms"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, rows))
	for _, predicate := range []string{"a @> '{2}'::integer[]", "terms @@ 'cat <-> rat'::tsquery", "a @> '{2}'::integer[] AND terms @@ 'cat <-> rat'::tsquery"} {
		freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM ks_part.gin_heap WHERE "+predicate+" ORDER BY k,v", []string{"k", "v"}, []string{"INT4", "TEXT"}, matches))
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='ks_part.kill_gin'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}

func ksGinIndexCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select("SELECT ks_part.gin_index_check('ks_part.kill_gin'::regclass) AS checked", []string{"checked"}, []string{"VOID"}, [][]any{{""}}))
}

func ksGinControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want []ksGinSurvivor) {
	t.Helper()
	headers := ksGinCapture(t, p, c)
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if after := ksGinCapture(t, p, c); !reflect.DeepEqual(headers, after) {
		t.Fatal("Dump changed source tuple identities or headers", headers, after)
	}
	ksGinState(t, p, b, c, want)
	if again := fixture.ExtensionDump(t, p.Ctx, p.Native); again != dump {
		t.Fatal("repeated read-only Dump changed captured GIN state")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal("unmodified killed-row GIN snapshot must Restore", err)
	}
	ksGinState(t, p, b, c, want)
	ksGinIndexCheck(t, p, b, c)
	ksGinPlacement(t, p, b, p.Native, c, dump, want)
	other, target, recorder := pgcryptoControlDestination(t, p, t.Name()+"/independent-gin")
	bb := b
	bb.Recorder = recorder
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal("independent Driver unmodified killed-row GIN snapshot", err)
	}
	ksGinState(t, p, bb, target, want)
	ksGinIndexCheck(t, p, bb, target)
	ksGinPlacement(t, p, bb, other, target, dump, want)
	freezeRun(t, p, bb, target, fixture.Exec("DELETE FROM ks_part.gin_heap", int64(len(want))))
	ksGinState(t, p, bb, target, nil)
	ksGinIndexCheck(t, p, bb, target)
	ksGinState(t, p, b, c, want) // Independent destination writes cannot leak back.
	ksGinIndexCheck(t, p, b, c)
}

// Isolate each exported physical version using ordinary DELETE. A main member
// must leave no pending work; a pending member must leave genuine cleanable work.
// This distinguishes wrong-but-in-range ordinals, including NULL/empty members,
// without inspecting comments, inventing counters or relaxing Restore validation.
func ksGinPlacement(t *testing.T, p *fixture.Pair, b fixture.Backend, db *sql.DB, c *sql.Conn, dump string, want []ksGinSurvivor) {
	t.Helper()
	for _, row := range want {
		if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(dump)); err != nil {
			t.Fatal("placement replay", err)
		}
		freezeRun(t, p, b, c, fixture.Exec("DELETE FROM ks_part.gin_heap WHERE k<>$1 OR v<>$2", int64(len(want)-1), row.k, row.v))
		ksGinState(t, p, b, c, []ksGinSurvivor{row})
		ksGinIndexCheck(t, p, b, c)
		const clean = "SELECT gin_clean_pending_list('ks_part.kill_gin'::regclass)>0 AS cleaned"
		freezeRun(t, p, b, c, fixture.Select(clean, []string{"cleaned"}, []string{"BOOL"}, [][]any{{row.pending}}))
		freezeRun(t, p, b, c, fixture.Prepared(fixture.Select(clean, []string{"cleaned"}, []string{"BOOL"}, [][]any{{false}})))
		ksGinState(t, p, b, c, []ksGinSurvivor{row})
		ksGinIndexCheck(t, p, b, c)
	}
	if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(dump)); err != nil {
		t.Fatal("restore complete survivor bag after placement witnesses", err)
	}
	ksGinState(t, p, b, c, want)
	ksGinIndexCheck(t, p, b, c)
}
