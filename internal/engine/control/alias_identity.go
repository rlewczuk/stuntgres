package control

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// planAliasIdentities runs under the catalog lock. It consumes only the ordinary
// allocator's monotonic outputs, never invokes a comparator, and leaves source
// descriptors and retained TEMP identities untouched.
func planAliasIdentities(ctx context.Context, next *catalog.State, retained, reserved map[ids.OID]bool, constraints []catalog.AliasOrderConstraint, allocate func(ids.OID) (ids.OID, error)) (map[ids.ObjectAddress]ids.ObjectAddress, error) {
	if len(constraints) == 0 {
		return nil, nil
	}
	affected := false
	for _, edge := range constraints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, point := range [2]catalog.AliasOrderEndpoint{edge.Lower, edge.Upper} {
			if point.Object != (ids.ObjectAddress{}) && retained[point.OID] {
				if d := next.Get(point.Object); d != nil && movableImportedIdentity(next, d) {
					affected = true
					break
				}
			}
		}
		if affected {
			break
		}
	}
	if !affected {
		return nil, ctx.Err()
	}
	type node struct {
		point  catalog.AliasOrderEndpoint
		parent int
	}
	var nodes []node
	index := make(map[catalog.AliasOrderEndpoint]int)
	key := func(p catalog.AliasOrderEndpoint) catalog.AliasOrderEndpoint {
		if p.Object != (ids.ObjectAddress{}) {
			p.OID = 0
			p.Domain = 0
		}
		return p
	}
	add := func(p catalog.AliasOrderEndpoint) int {
		k := key(p)
		if i, ok := index[k]; ok {
			return i
		}
		i := len(nodes)
		index[k] = i
		nodes = append(nodes, node{p, i})
		return i
	}
	var edges [][2]int
	for _, edge := range constraints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		edges = append(edges, [2]int{add(edge.Lower), add(edge.Upper)})
	}
	var root func(int) int
	root = func(i int) int {
		for nodes[i].parent != i {
			nodes[i].parent = nodes[nodes[i].parent].parent
			i = nodes[i].parent
		}
		return i
	}
	for _, edge := range edges {
		if nodes[edge[0]].point.OID == nodes[edge[1]].point.OID {
			a, b := root(edge[0]), root(edge[1])
			nodes[b].parent = a
		}
	}
	type group struct {
		old, lo, hi               ids.OID
		addresses                 []ids.ObjectAddress
		fixed, affected, assigned bool
		value                     ids.OID
	}
	groups := make([]group, len(nodes))
	for i, n := range nodes {
		g := &groups[root(i)]
		g.old = n.point.OID
		g.hi = ^ids.OID(0)
		d := next.Get(n.point.Object)
		if n.point.Object == (ids.ObjectAddress{}) || d == nil || !movableImportedIdentity(next, d) {
			g.fixed = true
		} else {
			g.addresses = append(g.addresses, n.point.Object)
			g.affected = g.affected || retained[n.point.OID]
		}
	}
	for i := range edges {
		edges[i] = [2]int{root(edges[i][0]), root(edges[i][1])}
	}
	// Only components joined to an actual endpoint collision enter planning.
	for changed := true; changed; {
		changed = false
		for _, e := range edges {
			if groups[e[0]].affected != groups[e[1]].affected {
				groups[e[0]].affected = true
				groups[e[1]].affected = true
				changed = true
			}
		}
	}
	var variables []int
	var boundaries []ids.OID
	for i := range groups {
		if root(i) != i {
			continue
		}
		g := &groups[i]
		if g.fixed || !g.affected {
			g.lo = g.old
			g.hi = g.old
			g.value = g.old
			g.assigned = true
		} else {
			variables = append(variables, i)
		}
		if g.affected {
			boundaries = append(boundaries, g.old)
		}
	}
	if len(variables) == 0 {
		return nil, nil
	}
	// Propagate genuine fixed-anchor bounds before acquiring slots. This avoids
	// jumping the allocator to intervals no endpoint can legally occupy.
	for changed := true; changed; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changed = false
		for _, e := range edges {
			if e[0] == e[1] || !groups[e[0]].affected {
				continue
			}
			a, b := &groups[e[0]], &groups[e[1]]
			if a.lo == ^ids.OID(0) || b.hi == 0 {
				return nil, aliasIdentityNoFit(a.old, b.old)
			}
			if b.lo < a.lo+1 {
				b.lo = a.lo + 1
				changed = true
			}
			if a.hi > b.hi-1 {
				a.hi = b.hi - 1
				changed = true
			}
			if a.lo > a.hi || b.lo > b.hi {
				return nil, aliasIdentityNoFit(a.old, b.old)
			}
		}
	}
	// Ordinary remints are part of the same complete demand. Their allocations
	// may use any compatible slot owned by this operation, without order edges.
	represented := make(map[ids.ObjectAddress]bool)
	for _, i := range variables {
		for _, address := range groups[i].addresses {
			represented[address] = true
		}
	}
	if err := visitTemporaryCollisions(ctx, next, retained, func(address ids.ObjectAddress) error {
		if represented[address] {
			return nil
		}
		represented[address] = true
		variables = append(variables, len(groups))
		groups = append(groups, group{old: address.ObjectID, lo: 1, hi: ^ids.OID(0), addresses: []ids.ObjectAddress{address}, affected: true})
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(variables, func(i, j int) bool {
		a, b := &groups[variables[i]], &groups[variables[j]]
		if a.old != b.old {
			return a.old < b.old
		}
		aa, bb := a.addresses[0], b.addresses[0]
		if aa.ClassID != bb.ClassID {
			return aa.ClassID < bb.ClassID
		}
		return aa.SubID < bb.SubID
	})
	// Every fresh output remains owned by this one complete replacement plan.
	// Retain choices belong only to their original identity, never to remints.
	var pool []ids.OID
	used := make(map[ids.OID]bool)
	available := make(map[ids.OID]bool)
	compatible := func() bool {
		for _, e := range edges {
			a, b := &groups[e[0]], &groups[e[1]]
			if e[0] != e[1] && a.assigned && b.assigned && a.value >= b.value {
				return false
			}
		}
		return true
	}
	var search func(int) (bool, error)
	search = func(at int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if at == len(variables) {
			return compatible(), nil
		}
		// A union-of-remaining-options capacity bound is safe even across shared
		// anchor intervals. It prunes exhaustion, never a greedy scheduling choice.
		clear(available)
		for _, i := range variables[at:] {
			g := &groups[i]
			if !retained[g.old] && g.old >= g.lo && g.old <= g.hi && !used[g.old] {
				available[g.old] = true
			}
			for _, id := range pool {
				if id >= g.lo && id <= g.hi && !used[id] {
					available[id] = true
				}
			}
		}
		if len(available) < len(variables)-at {
			return false, nil
		}
		i := variables[at]
		g := &groups[i]
		for candidate := -1; candidate < len(pool); candidate++ {
			id := g.old
			if candidate < 0 {
				if retained[id] {
					continue
				}
			} else {
				id = pool[candidate]
			}
			if id < g.lo || id > g.hi || used[id] {
				continue
			}
			g.value = id
			g.assigned = true
			used[id] = true
			if compatible() {
				ok, err := search(at + 1)
				if ok || err != nil {
					return ok, err
				}
			}
			delete(used, id)
			g.assigned = false
		}
		return false, nil
	}
	// Partition only at real fixed/retain boundaries and propagated domain
	// limits. N earliest eligible slots per interval suffice for N total demand
	// groups, including ordinary remints. Sparse skips use the same allocator.
	for _, i := range variables {
		if groups[i].lo != 0 {
			boundaries = append(boundaries, groups[i].lo)
		}
		boundaries = append(boundaries, groups[i].hi)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	type interval struct {
		lo, hi   ids.OID
		acquired int
	}
	var intervals []interval
	addInterval := func(lo, hi ids.OID) {
		if lo == 0 || lo > hi {
			return
		}
		for _, i := range variables {
			if groups[i].lo <= hi && groups[i].hi >= lo {
				intervals = append(intervals, interval{lo: lo, hi: hi})
				return
			}
		}
	}
	start := ids.OID(1)
	for _, boundary := range boundaries {
		if boundary < start {
			continue
		}
		if boundary > start {
			addInterval(start, boundary-1)
		}
		addInterval(boundary, boundary)
		if boundary == ^ids.OID(0) {
			start = 0
			break
		}
		start = boundary + 1
	}
	if start != 0 {
		addInterval(start, ^ids.OID(0))
	}
	solved := false
	current := 0
	for {
		var err error
		// Search the complete problem before every irreversible acquisition.
		solved, err = search(0)
		if err != nil {
			return nil, err
		}
		if solved {
			break
		}
		for current < len(intervals) && intervals[current].acquired >= len(variables) {
			current++
		}
		if current == len(intervals) {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := allocate(intervals[current].lo)
		if errors.Is(err, catalog.ErrObjectIdentityExhausted) {
			break
		}
		if err != nil {
			return nil, err
		}
		// The actual frontier may already exceed this interval. Classify the
		// returned output immediately; eligible higher outputs are owned too.
		for current < len(intervals) && id > intervals[current].hi {
			current++
		}
		if current == len(intervals) {
			break
		}
		if id < intervals[current].lo || reserved[id] {
			continue
		}
		intervals[current].acquired++
		pool = append(pool, id)
	}
	if !solved {
		// Capacity can be shared across components, so report the actual joint
		// restrictions rather than blame an arbitrary otherwise-feasible edge.
		var restrictions [][2]ids.OID
		for _, e := range edges {
			if groups[e[0]].affected {
				restrictions = append(restrictions, [2]ids.OID{groups[e[0]].old, groups[e[1]].old})
			}
		}
		sort.Slice(restrictions, func(i, j int) bool {
			if restrictions[i][0] != restrictions[j][0] {
				return restrictions[i][0] < restrictions[j][0]
			}
			return restrictions[i][1] < restrictions[j][1]
		})
		return nil, fmt.Errorf("stuntgres: imported alias range ordering is unsatisfiable for endpoint identity constraints %v: %w", restrictions, sqlError("22000", "range lower bound must be less than or equal to range upper bound"))
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, i := range variables {
		g := &groups[i]
		if g.value == g.old {
			continue
		}
		reserved[g.value] = true
		for _, a := range g.addresses {
			target := a
			target.ObjectID = g.value
			mapping[a] = target
		}
	}
	return mapping, nil
}

// Preserve the existing Restore adapter's range-data diagnostic. This is an
// exhausted structural assignment proof, not a synthesized source SQL failure.
func aliasIdentityNoFit(lower, upper ids.OID) error {
	return fmt.Errorf("stuntgres: imported alias range ordering is unsatisfiable for endpoint identities %d and %d: %w", lower, upper, sqlError("22000", "range lower bound must be less than or equal to range upper bound"))
}
