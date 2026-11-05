package reference_test

import (
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// pgDropAddress is a pg_depend object address.
type pgDropAddress struct {
	class, oid int64
	sub        int32
}

// pgDropEdge is a recorded dependency between two named graph nodes.
type pgDropEdge struct {
	dep, ref string
	kind     byte
}

// pgDropGraph is an explicitly specified dependency graph. Node identities and
// descriptions are observed; the edges are written by the test and checked
// against pg_depend, so the expectation never derives from a DROP result.
type pgDropGraph struct {
	address map[string]pgDropAddress
	label   map[string]string
	edges   []pgDropEdge
}

// hstoreDropGraphOf resolves each node expression, which yields classid,
// objid and objsubid, and checks that pg_depend records exactly edges among
// the nodes: every non-pinned dependency of a node object names another node.
// A node whose expression yields a NULL identity is absent, with its edges.
// The walk can then reach a node only through the listed edges. The scenario
// schema is not dropped, so references to it never enter the walk.
func hstoreDropGraphOf(t *testing.T, p *fixture.Pair, db fixture.Executor, nodes [][2]string, edges []pgDropEdge) pgDropGraph {
	t.Helper()
	g := pgDropGraph{address: map[string]pgDropAddress{}, label: map[string]string{}, edges: edges}
	names := map[pgDropAddress]string{}
	var wholes []string
	for _, node := range nodes {
		var a pgDropAddress
		var label string
		var oid sql.NullInt64
		q := `SELECT x.c::bigint, x.o::bigint, x.s::integer, COALESCE(pg_describe_object(x.c, x.o, x.s), '') FROM (SELECT ` + node[1] + `) AS x(c, o, s)`
		rows, err := db.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatalf("resolve %s: %v", node[0], err)
		}
		if !rows.Next() {
			t.Fatalf("resolve %s: no row %v", node[0], rows.Err())
		}
		err = rows.Scan(&a.class, &oid, &a.sub, &label)
		rows.Close()
		if err != nil {
			t.Fatalf("resolve %s: %v", node[0], err)
		}
		// An optional node (a NULL identity) is absent with all its edges.
		if !oid.Valid {
			continue
		}
		a.oid = oid.Int64
		g.address[node[0]], g.label[node[0]], names[a] = a, label, node[0]
		wholes = append(wholes, fmt.Sprintf("(%d,%d)", a.class, a.oid))
	}
	rows, err := db.QueryContext(p.Ctx, `SELECT classid::bigint, objid::bigint, objsubid, refclassid::bigint, refobjid::bigint, refobjsubid, deptype::text FROM pg_depend WHERE refobjid >= 16384 AND refclassid <> 'pg_namespace'::regclass AND (classid::bigint, objid::bigint) IN (`+strings.Join(wholes, ",")+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var dep, ref pgDropAddress
		var kind string
		if err := rows.Scan(&dep.class, &dep.oid, &dep.sub, &ref.class, &ref.oid, &ref.sub, &kind); err != nil {
			t.Fatal(err)
		}
		d, ok := names[dep]
		if !ok {
			d = fmt.Sprintf("unlisted %v", dep)
		}
		r, ok := names[ref]
		if !ok {
			r = fmt.Sprintf("unlisted %v", ref)
		}
		got = append(got, d+" -"+kind+"-> "+r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	g.edges = nil
	for _, e := range edges {
		if _, ok := g.address[e.dep]; !ok {
			continue
		}
		if _, ok := g.address[e.ref]; !ok {
			continue
		}
		g.edges = append(g.edges, e)
	}
	var want []string
	for _, e := range g.edges {
		want = append(want, e.dep+" -"+string(e.kind)+"-> "+e.ref)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("pg_depend graph\ngot  %q\nwant %q", got, want)
	}
	return g
}

const (
	pgDropOriginal = 1 << iota
	pgDropNormal
	pgDropAuto
	pgDropInternal
	pgDropExtension
	pgDropReverse
	pgDropSubobject
)

// drop applies PostgreSQL 18 findDependentObjects to roots, in order, and
// returns the RESTRICT detail lines and CASCADE notice lines that
// reportDependentObjects derives from the resulting target list.
func (g pgDropGraph) drop(roots ...string) (restrict, cascade []string) {
	type entry struct {
		name     string
		flags    int
		dependee string
	}
	var targets []*entry
	type frame struct {
		name  string
		flags *int
	}
	var stack []frame
	pending := map[string]bool{}
	for _, root := range roots {
		pending[root] = true
	}
	same := func(a, b string) (exact, whole bool) {
		x, y := g.address[a], g.address[b]
		return x == y, x.class == y.class && x.oid == y.oid
	}
	// The present helpers follow object_address_present_add_flags and
	// stack_address_present_add_flags, including whole/subobject matches.
	present := func(name string, flags int, holder func(i int) (string, *int), n int) bool {
		found := false
		for i := n - 1; i >= 0; i-- {
			other, otherFlags := holder(i)
			exact, whole := same(name, other)
			switch {
			case exact:
				*otherFlags |= flags
				found = true
			case whole && g.address[other].sub == 0:
				found = true
			case whole && g.address[name].sub == 0 && flags != 0:
				*otherFlags |= flags | pgDropSubobject
			}
		}
		return found
	}
	inTargets := func(name string, flags int) bool {
		return present(name, flags, func(i int) (string, *int) { return targets[i].name, &targets[i].flags }, len(targets))
	}
	inStack := func(name string, flags int) bool {
		return present(name, flags, func(i int) (string, *int) { return stack[i].name, stack[i].flags }, len(stack))
	}
	var find func(string, int)
	find = func(name string, flags int) {
		if inStack(name, flags) || inTargets(name, flags) {
			return
		}
		self := g.address[name]
		// At the outermost level every owner is checked first: a pending
		// owner deletes this object later, otherwise the DROP is refused.
		if len(stack) == 0 {
			var owners []string
			for _, e := range g.edges {
				dep := g.address[e.dep]
				if dep.class == self.class && dep.oid == self.oid && (self.sub == 0 || dep.sub == self.sub) && (e.kind == 'i' || e.kind == 'e') {
					if pending[e.ref] {
						return
					}
					owners = append(owners, e.ref)
				}
			}
			if len(owners) != 0 {
				panic("drop of " + name + " requires " + owners[0])
			}
		}
		for _, e := range g.edges {
			dep := g.address[e.dep]
			if dep.class != self.class || dep.oid != self.oid || self.sub != 0 && dep.sub != self.sub || e.kind != 'i' && e.kind != 'e' {
				continue
			}
			if inStack(e.ref, 0) {
				continue
			}
			// An unentered owner is deleted instead; this object is not
			// stacked, so the owner keeps the current caller.
			find(e.ref, pgDropReverse)
			if !inTargets(name, flags) {
				panic("owner " + e.ref + " did not delete " + name)
			}
			return
		}
		type dependent struct {
			name  string
			flags int
		}
		var dependents []dependent
		for _, e := range g.edges {
			dep, ref := g.address[e.dep], g.address[e.ref]
			if ref.class != self.class || ref.oid != self.oid || self.sub != 0 && ref.sub != self.sub || dep.class == self.class && dep.oid == self.oid && self.sub == 0 {
				continue
			}
			kind := map[byte]int{'n': pgDropNormal, 'a': pgDropAuto, 'x': pgDropAuto, 'i': pgDropInternal, 'e': pgDropExtension}[e.kind]
			dependents = append(dependents, dependent{e.dep, kind})
		}
		// object_address_comparator: OID descending, class, unsigned subid.
		sort.SliceStable(dependents, func(i, j int) bool {
			a, b := g.address[dependents[i].name], g.address[dependents[j].name]
			if a.oid != b.oid {
				return a.oid > b.oid
			}
			if a.class != b.class {
				return a.class < b.class
			}
			return uint32(a.sub) < uint32(b.sub)
		})
		current := flags
		stack = append(stack, frame{name, &current})
		for _, d := range dependents {
			find(d.name, d.flags)
		}
		stack = stack[:len(stack)-1]
		e := &entry{name: name, flags: current}
		if len(stack) != 0 {
			e.dependee = stack[len(stack)-1].name
		}
		targets = append(targets, e)
	}
	for _, root := range roots {
		find(root, pgDropOriginal)
	}
	for i := len(targets) - 1; i >= 0; i-- {
		e := targets[i]
		if e.flags&(pgDropOriginal|pgDropSubobject) != 0 || e.flags&(pgDropAuto|pgDropInternal|pgDropExtension) != 0 {
			continue
		}
		restrict = append(restrict, g.label[e.name]+" depends on "+g.label[e.dependee])
		cascade = append(cascade, g.label[e.name])
	}
	return restrict, cascade
}
