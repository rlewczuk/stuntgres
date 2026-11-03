package index

import (
	"context"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// GinMaintenance owns one fallible immutable preparation, never a persistent
// registry. Memoization is confined to the captured alias cohort.
type GinMaintenance struct {
	Context        context.Context
	Index          *catalog.Relation
	CommittedIndex *catalog.Relation
	Store          *storage.Relation
	Input          sqltype.InputEnvironment
	View           catalog.Read
	TargetView     catalog.Read
	Roots          map[ids.ObjectAddress]*storage.IndexRoot
	Stores         map[ids.ObjectAddress]*storage.Relation
	roots          map[*storage.IndexRoot]ginMaintenanceRoot
	journals       map[*session.Journal]*session.Journal
	rows           map[ginRowsKey][]storage.RowVersion
	history        map[*storage.HeapPredecessor]*storage.HeapPredecessor
	bindings       map[*catalog.Relation]ginMaintenanceBinding
	authorities    map[*session.Journal]GinMaintenanceAuthority
}
type ginRowsKey struct {
	first  *storage.RowVersion
	length int
}
type ginMaintenanceRoot struct {
	root       *storage.IndexRoot
	incidences int64
}
type ginMaintenanceBinding struct {
	columns []sqltype.GinBinding
	target  *catalog.Relation
}

func (m *GinMaintenance) Root(root *storage.IndexRoot) (*storage.IndexRoot, error) {
	if root == nil || root.Gin == nil {
		return root, m.Context.Err()
	}
	if next, ok := m.roots[root]; ok {
		return next.root, nil
	}
	payload, incidences, err := storage.CleanGin(m.Context, root.Gin, m.Input)
	if err != nil {
		return nil, err
	}
	next := root
	if payload != root.Gin {
		copy := *root
		copy.Gin = payload
		next = &copy
	}
	if m.roots == nil {
		m.roots = make(map[*storage.IndexRoot]ginMaintenanceRoot)
	}
	m.roots[root] = ginMaintenanceRoot{root: next, incidences: incidences}
	return next, nil
}
func (m *GinMaintenance) PendingIncidences(root *storage.IndexRoot) int64 {
	return m.roots[root].incidences
}
func (m *GinMaintenance) version(row storage.RowVersion) (storage.RowVersion, bool, error) {
	if err := m.Context.Err(); err != nil {
		return row, false, err
	}
	changed := false
	if output, ok := row.IndexOutputs[m.Index.Address()]; ok {
		_, target, accepted, err := m.OutputBindings(output, row.IndexAdmission)
		if err != nil {
			return row, false, err
		}
		if accepted && (output.GinPending || output.Index != target) {
			row.IndexOutputs = maps.Clone(row.IndexOutputs)
			output.GinPending, output.Index = false, target
			row.IndexOutputs[m.Index.Address()] = output
			changed = true
		}
	}
	prior := row.PrivateHistory
	if prior != nil {
		next, ok := m.history[prior]
		if !ok {
			var err error
			next, err = storage.MapHeapPredecessors(prior, m.version)
			if err != nil {
				return row, false, err
			}
			if m.history == nil {
				m.history = make(map[*storage.HeapPredecessor]*storage.HeapPredecessor)
			}
			m.history[prior] = next
		}
		row.PrivateHistory = next
		changed = changed || next != prior
	}
	return row, changed, nil
}
func (m *GinMaintenance) Rows(rows []storage.RowVersion) ([]storage.RowVersion, error) {
	if len(rows) == 0 {
		return rows, m.Context.Err()
	}
	key := ginRowsKey{&rows[0], len(rows)}
	if next, ok := m.rows[key]; ok {
		return next, nil
	}
	next := rows
	copied := false
	for i, row := range rows {
		mapped, changed, err := m.version(row)
		if err != nil {
			return nil, err
		}
		if changed {
			if !copied {
				next, copied = slices.Clone(rows), true
			}
			next[i] = mapped
		}
	}
	if m.rows == nil {
		m.rows = make(map[ginRowsKey][]storage.RowVersion)
	}
	m.rows[key] = next
	return next, nil
}
func (m *GinMaintenance) Writes(writes map[ids.ObjectAddress][]storage.RowVersion) (map[ids.ObjectAddress][]storage.RowVersion, error) {
	rows := writes[m.Index.SQLIndex.Parent]
	next, err := m.Rows(rows)
	if err != nil {
		return nil, err
	}
	if storage.SameVersionRoot(rows, next) {
		return writes, nil
	}
	out := maps.Clone(writes)
	out[m.Index.SQLIndex.Parent] = next
	return out, nil
}
func (m *GinMaintenance) Journal(j *session.Journal) (*session.Journal, error) {
	if j == nil {
		return nil, m.Context.Err()
	}
	if next, ok := m.journals[j]; ok {
		return next, nil
	}
	next := j
	slot := JournalIndexesOrZero(j)
	candidate, found := slot.Candidates[m.Index.Address()]
	unique := slot.UniqueRoots[m.Index.Address()]
	var err error
	authority, captured := m.authorities[j]
	_, bound := m.bindings[candidate.Index]
	accepted := found && captured && authority.matched && candidate == authority.candidate && bound
	if accepted {
		candidate.Root, err = m.Root(candidate.Root)
		if err != nil {
			return nil, err
		}
		candidate.routineSource, err = m.receipt(candidate.routineSource, candidate)
		if err != nil {
			return nil, err
		}
	}
	mapped := unique
	if accepted {
		mapped, err = m.Root(unique)
	}
	if err != nil {
		return nil, err
	}
	if found && candidate != slot.Candidates[m.Index.Address()] || mapped != unique {
		next = j.Clone()
		owner := JournalIndexes(next)
		if found {
			owner.Candidates[m.Index.Address()] = candidate
		}
		if mapped != unique {
			owner.UniqueRoots[m.Index.Address()] = mapped
		}
	}
	if m.journals == nil {
		m.journals = make(map[*session.Journal]*session.Journal)
	}
	m.journals[j] = next
	return next, nil
}

// A live receipt is transported only through this certified representation
// transition. Detached receipts retain their old root pins and still reject.
func (m *GinMaintenance) receipt(old *routineRootSource, candidate RootCandidate) (*routineRootSource, error) {
	if old == nil {
		return nil, nil
	}
	pins := old.pins
	if pins == nil {
		return nil, ids.ErrCatalogReplaced
	}
	nextPins := *pins
	if pins.address == m.Index.Address() {
		root, err := m.Root(pins.root)
		if err != nil {
			return nil, err
		}
		nextPins.root = root
	}
	plain := candidate
	plain.routineSource = nil
	next := *old
	next.pins, next.candidate = &nextPins, plain
	if old.previous != nil {
		prior := old.previous.candidate
		var err error
		prior.Root, err = m.Root(prior.Root)
		if err != nil {
			return nil, err
		}
		next.previous, err = m.receipt(old.previous, prior)
		if err != nil {
			return nil, err
		}
	}
	if nextPins == *pins && plain == old.candidate && next.previous == old.previous {
		return old, nil
	}
	return &next, nil
}

// ReconcileGinWrites rebases stale lexical aliases against the latest accepted
// owner versions. Main placement wins only for the identical version/index.
func ReconcileGinWrites(ctx context.Context, stale, current map[ids.ObjectAddress][]storage.RowVersion) (map[ids.ObjectAddress][]storage.RowVersion, error) {
	out := stale
	copied := false
	for address, rows := range stale {
		authority := make(map[RowIdentity]storage.RowVersion, len(current[address]))
		var capture func(storage.RowVersion)
		capture = func(row storage.RowVersion) {
			authority[RowIdentity{Row: row.Row, Version: row.Version}] = row
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				capture(prior.Version)
			}
		}
		for _, row := range current[address] {
			capture(row)
		}
		var normalize func(storage.RowVersion) (storage.RowVersion, bool, error)
		normalize = func(row storage.RowVersion) (storage.RowVersion, bool, error) {
			if err := ctx.Err(); err != nil {
				return row, false, err
			}
			changed := false
			original := authority[RowIdentity{Row: row.Row, Version: row.Version}]
			for key, output := range row.IndexOutputs {
				accepted, ok := original.IndexOutputs[key]
				if ok && output.GinPending && !accepted.GinPending && accepted.Member && OutputMatches(accepted, output.Index) {
					if !changed {
						row.IndexOutputs = maps.Clone(row.IndexOutputs)
						changed = true
					}
					output.GinPending = false
					row.IndexOutputs[key] = output
				}
			}
			history, err := storage.MapHeapPredecessors(row.PrivateHistory, normalize)
			changed = changed || history != row.PrivateHistory
			row.PrivateHistory = history
			return row, changed, err
		}
		next := rows
		cloned := false
		for i, row := range rows {
			mapped, changed, err := normalize(row)
			if err != nil {
				return nil, err
			}
			if changed {
				if !cloned {
					next, cloned = slices.Clone(rows), true
				}
				next[i] = mapped
			}
		}
		if cloned {
			if !copied {
				out, copied = maps.Clone(stale), true
			}
			out[address] = next
		}
	}
	return out, ctx.Err()
}

// ReconcileGinJournal transports only aliases of the exact accepted candidate:
// cleanup shares Entries backing and all source/store pins. A genuinely newer
// build or different source is not an alias and retains its ordinary fences.
func ReconcileGinJournal(stale, current *session.Journal) *session.Journal {
	if stale == nil || current == nil || stale == current {
		return stale
	}
	out := stale
	x, y := JournalIndexesOrZero(stale), JournalIndexesOrZero(current)
	sameEntries := func(a, b *storage.IndexRoot) bool {
		return a != nil && b != nil && a.Gin != nil && b.Gin != nil && len(a.Entries) == len(b.Entries) && (len(a.Entries) == 0 || &a.Entries[0] == &b.Entries[0])
	}
	for address, prior := range x.Candidates {
		next, ok := y.Candidates[address]
		if !ok || prior == next || prior.Index != next.Index || prior.Storage != next.Storage || prior.Generation != next.Generation || prior.Ends != next.Ends || prior.Length != next.Length || !sameEntries(prior.Root, next.Root) || storage.GinPendingIncidences(next.Root.Gin) != 0 {
			continue
		}
		if out == stale {
			out = stale.Clone()
		}
		JournalIndexes(out).Candidates[address] = next
		if sameEntries(x.UniqueRoots[address], next.Root) {
			JournalIndexes(out).UniqueRoots[address] = next.Root
		}
	}
	return out
}
