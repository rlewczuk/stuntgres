package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"maps"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The identity scenario combines extension-owned types, their generated
// arrays, routines and an operator with views whose _RETURN rules reach them
// through several parents. Domain hs_id_dom is not a member: its array, which
// hs_id_ddom consumes, is reached from its entered owner. The extension's own
// array, which hs_id_adom consumes, can be reached before its element type
// (natively it is the newest member).
var hstoreDropIdentitySeed = []string{
	"CREATE DOMAIN hs_id_dom AS hstore",
	"CREATE TABLE hs_id_t(v hstore)",
	"INSERT INTO hs_id_t VALUES ('k=>1')",
	"CREATE DOMAIN hs_id_adom AS hstore[]",
	"CREATE DOMAIN hs_id_ddom AS hs_id_dom[]",
	"CREATE TABLE hs_id_keep(x integer)",
	"CREATE VIEW hs_id_col AS SELECT v FROM hs_id_t",
	"CREATE VIEW hs_id_op AS SELECT v -> 'k' AS k FROM hs_id_t",
	"CREATE VIEW hs_id_typ AS SELECT v, NULL::hstore AS n FROM hs_id_t",
}

func hstoreDropIdentitySeedRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range hstoreDropIdentitySeed {
		var n int64
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		hstoreRun(t, p, b, c, fixture.Exec(q, n))
	}
}

func hstoreDropIdentityNodes() [][2]string {
	object := func(catalog, oid string) string { return "'" + catalog + "'::regclass, " + oid + ", 0" }
	rule := func(view string) string {
		return object("pg_rewrite", "(SELECT oid FROM pg_rewrite WHERE ev_class='"+view+"'::regclass)")
	}
	column := func(relation string, n int) string {
		return fmt.Sprintf("'pg_class'::regclass, '%s'::regclass, %d", relation, n)
	}
	return [][2]string{
		{"ext", object("pg_extension", "(SELECT oid FROM pg_extension WHERE extname='hstore')")},
		{"type", object("pg_type", "'hstore'::regtype")},
		{"array", object("pg_type", "'hstore[]'::regtype")},
		{"in", object("pg_proc", "'hstore_in(cstring)'::regprocedure")},
		{"out", object("pg_proc", "'hstore_out(hstore)'::regprocedure")},
		// The native manifest has no binary I/O routines yet.
		{"recv", object("pg_proc", "to_regprocedure('hstore_recv(internal)')")},
		{"send", object("pg_proc", "to_regprocedure('hstore_send(hstore)')")},
		{"handler", object("pg_proc", "'hstore_subscript_handler(internal)'::regprocedure")},
		{"fetchval", object("pg_proc", "'fetchval(hstore,text)'::regprocedure")},
		{"op", object("pg_operator", "'->(hstore,text)'::regoperator")},
		{"dom", object("pg_type", "'hs_id_dom'::regtype")},
		{"domarray", object("pg_type", "'hs_id_dom[]'::regtype")},
		{"t.v", column("hs_id_t", 1)},
		{"adom", object("pg_type", "'hs_id_adom'::regtype")},
		{"ddom", object("pg_type", "'hs_id_ddom'::regtype")},
		{"col", object("pg_class", "'hs_id_col'::regclass")},
		{"col.v", column("hs_id_col", 1)},
		{"col.rule", rule("hs_id_col")},
		{"op.view", object("pg_class", "'hs_id_op'::regclass")},
		{"op.rule", rule("hs_id_op")},
		{"typ", object("pg_class", "'hs_id_typ'::regclass")},
		{"typ.v", column("hs_id_typ", 1)},
		{"typ.n", column("hs_id_typ", 2)},
		{"typ.rule", rule("hs_id_typ")},
	}
}

// hstoreDropIdentityEdges are the scenario's dependencies, per PostgreSQL 18
// pg_type.c, pg_proc.c, pg_operator.c, heap.c and the view rule's expression
// references. A domain shares its base type's output and send routines.
var hstoreDropIdentityEdges = []pgDropEdge{
	{"type", "ext", 'e'}, {"type", "in", 'n'}, {"type", "out", 'n'}, {"type", "recv", 'n'}, {"type", "send", 'n'}, {"type", "handler", 'n'},
	{"array", "ext", 'e'}, {"array", "type", 'i'},
	{"in", "ext", 'e'}, {"in", "type", 'n'},
	{"out", "ext", 'e'}, {"out", "type", 'n'},
	{"recv", "ext", 'e'}, {"recv", "type", 'n'},
	{"send", "ext", 'e'}, {"send", "type", 'n'},
	{"handler", "ext", 'e'},
	{"fetchval", "ext", 'e'}, {"fetchval", "type", 'n'},
	{"op", "ext", 'e'}, {"op", "type", 'n'}, {"op", "fetchval", 'n'},
	{"dom", "type", 'n'}, {"dom", "out", 'n'}, {"dom", "send", 'n'}, {"domarray", "dom", 'i'},
	{"t.v", "type", 'n'}, {"adom", "array", 'n'}, {"ddom", "domarray", 'n'},
	{"col.v", "type", 'n'}, {"col.rule", "col", 'i'}, {"col.rule", "t.v", 'n'},
	{"op.rule", "op.view", 'i'}, {"op.rule", "t.v", 'n'}, {"op.rule", "op", 'n'},
	{"typ.v", "type", 'n'}, {"typ.n", "type", 'n'}, {"typ.rule", "typ", 'i'}, {"typ.rule", "t.v", 'n'}, {"typ.rule", "type", 'n'},
}

func hstoreDropIdentityGraph(t *testing.T, p *fixture.Pair, db fixture.Executor) pgDropGraph {
	t.Helper()
	return hstoreDropGraphOf(t, p, db, hstoreDropIdentityNodes(), hstoreDropIdentityEdges)
}

// hstoreDropIdentityIntact proves a refused or rolled-back DROP kept every
// identity, dependency record, row and callable binding.
func hstoreDropIdentityIntact(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, want pgDropGraph) {
	t.Helper()
	if got := hstoreDropIdentityGraph(t, p, db); !maps.Equal(got.address, want.address) || !maps.Equal(got.label, want.label) {
		t.Fatalf("identities changed\ngot  %v\nwant %v", got.address, want.address)
	}
	for _, s := range []fixture.Step{
		fixture.Select(`SELECT k FROM hs_id_op`, []string{"k"}, []string{"TEXT"}, [][]any{{"1"}}),
		fixture.Select(`SELECT v::text AS v, n IS NULL AS n FROM hs_id_typ`, []string{"v", "n"}, []string{"TEXT", "BOOL"}, [][]any{{`"k"=>"1"`, true}}),
		fixture.Select(`SELECT ('a=>2'::hs_id_dom -> 'a') AS a`, []string{"a"}, []string{"TEXT"}, [][]any{{"2"}}),
	} {
		hstoreRun(t, p, b, db, s)
	}
}

func hstoreDropIdentityRestrict(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, g pgDropGraph) {
	t.Helper()
	detail, _ := g.drop("ext")
	for _, q := range []string{"DROP EXTENSION hstore", "DROP EXTENSION hstore RESTRICT"} {
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, db, q, prepared, false, false, hstoreDropRestrict(strings.Join(detail, "\n")))
			hstoreDropIdentityIntact(t, p, b, db, g)
		}
	}
}

// Several extension roots are walked in statement order; a later root is
// pending, not entered, while an earlier one is walked.
func hstoreDropIdentityMixedRoots(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("CREATE EXTENSION cube", 0))
	nodes := append(hstoreDropIdentityNodes(), [2]string{"cube", "'pg_extension'::regclass, (SELECT oid FROM pg_extension WHERE extname='cube'), 0"})
	g := hstoreDropGraphOf(t, p, c, nodes, hstoreDropIdentityEdges)
	for _, roots := range [][2]string{{"ext", "cube"}, {"cube", "ext"}} {
		detail, _ := g.drop(roots[0], roots[1])
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop desired object(s) because other objects depend on them")
		want.Detail, want.Hint = strings.Join(detail, "\n"), "Use DROP ... CASCADE to drop the dependent objects too."
		q := map[string]string{"ext": "hstore", "cube": "cube"}
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, "DROP EXTENSION "+q[roots[0]]+", "+q[roots[1]], prepared, false, false, want)
			hstoreDropIdentityIntact(t, p, b, c, hstoreDropIdentityGraph(t, p, c))
		}
	}
	hstoreRun(t, p, b, c, fixture.Exec("DROP EXTENSION cube", 0))
}

func TestReferenceHstoreDropIdentityGraph(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		hstoreInstall(t, p, b, c)
		hstoreDropIdentitySeedRun(t, p, b, c)
		g := hstoreDropIdentityGraph(t, p, c)
		hstoreDropIdentityRestrict(t, p, b, owner, g)
		hstoreDropCascaded(t, &notices)
		hstoreDropIdentityMixedRoots(t, p, b, c)
		_, cascade := g.drop("ext")
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			s := fixture.Exec("DROP EXTENSION hstore CASCADE", 0)
			s.Prepared = prepared
			run(s)
			hstoreDropCascaded(t, &notices, cascade...)
			run(fixture.Select(`SELECT count(*) AS n FROM pg_class WHERE relname IN ('hs_id_col','hs_id_op','hs_id_typ')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("ROLLBACK", 0))
			hstoreDropIdentityIntact(t, p, b, owner, g)
		}
		run(fixture.Exec("DROP EXTENSION hstore CASCADE", 0))
		hstoreDropCascaded(t, &notices, cascade...)
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT relname::text AS r, (SELECT count(*) FROM pg_attribute WHERE attrelid=c.oid AND attnum>0 AND NOT attisdropped) AS n FROM pg_class c WHERE relname LIKE 'hs\_id\_%' ORDER BY 1`, []string{"r", "n"}, []string{"TEXT", "INT8"}, [][]any{{"hs_id_keep", int64(1)}, {"hs_id_t", int64(0)}}),
			fixture.Select(`SELECT count(*) AS n FROM hs_id_t`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT count(*) AS n FROM pg_type WHERE typname IN ('hstore','_hstore','hs_id_dom','_hs_id_dom','hs_id_adom','_hs_id_adom','hs_id_ddom','_hs_id_ddom')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		} {
			run(s)
		}
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Select(`SELECT 'a=>1'::hstore -> 'a' AS a`, []string{"a"}, []string{"TEXT"}, [][]any{{"1"}}))
	})
}

// Restore can remint view, rule and member identities independently. The
// expected report follows the identities observed after each restore.
func TestReferenceHstoreDropIdentityRestored(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreDropIdentitySeedRun(t, p, b, c)
		hstoreDropIdentityRestrict(t, p, b, c, hstoreDropIdentityGraph(t, p, c))
		if b.PG {
			return
		} // Reset/Dump/Restore are native public APIs only.
		dump := hstoreDump(t, p, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		hstoreDropIdentityRestrict(t, p, b, c, hstoreDropIdentityGraph(t, p, c))
		// Live TEMP objects own the destination's first identities, so the
		// restored extension and consumers collide and are given new ones.
		for _, collisions := range []int{7, 12} {
			destination := fixture.NewFixture(t, false, true)
			session, err := destination.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i := range collisions {
				if _, err := session.ExecContext(p.Ctx, fmt.Sprintf("CREATE TEMP TABLE hs_id_collision_%d(v integer)", i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
			hstoreDropIdentityRestrict(t, p, target, destination.Memory, hstoreDropIdentityGraph(t, p, destination.Memory))
			if after := hstoreDump(t, p, destination.Native); !bytes.Equal(dump, after) {
				t.Fatalf("remapped restore changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
			}
			session.Close()
		}
	})
}
