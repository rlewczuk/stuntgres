package table

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// VacuumCapture is the input of one VACUUM work pass, captured under
// Engine.mu: the heap's version root, end root and generation, the commit
// frontier, the freezing cutoff, whether dead heads may retire, the catalog
// state and relation, and the attached index roots.
type VacuumCapture struct {
	Rows       []storage.RowVersion
	Ends       *storage.DeletionRoot
	Generation uint64
	Frontier   ids.CommitSeq
	Cutoff     uint64
	Retire     bool
	Catalog    *catalog.State
	Relation   *catalog.Relation
	Indexes    map[ids.ObjectAddress]*storage.IndexRoot
	Attached   int
	IndexCount int
}

// CaptureIndexesLocked counts the indexes attached to c's relation in c's
// catalog, read through owner, and those with a root in roots; collect also
// records those roots. The final preflight counts existing roots without
// allocating a second map; only a stale capture needs a fresh manifest.
// Caller holds Engine.mu.
func (c *VacuumCapture) CaptureIndexesLocked(owner *catalog.Owner, roots map[ids.ObjectAddress]*storage.IndexRoot, collect bool) {
	c.Attached, c.IndexCount = 0, 0
	view := catalog.NewRead(owner, c.Catalog)
	for a, descriptor := range view.Descriptors() {
		if idx, ok := descriptor.(*catalog.Relation); ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == c.Relation.Address() {
			c.Attached++
			if root := roots[a]; root != nil {
				c.IndexCount++
				if collect {
					if c.Indexes == nil {
						c.Indexes = make(map[ids.ObjectAddress]*storage.IndexRoot)
					}
					c.Indexes[a] = root
				}
			}
		}
	}
}

// SameLocked reports whether current, captured again under Engine.mu after
// c's detached pass, still matches c, so the pass may install. A pass that
// rewrites the catalog also needs the same catalog state. Caller holds
// Engine.mu.
func (c VacuumCapture) SameLocked(current VacuumCapture, store *storage.Store, rewritesCatalog bool) bool {
	same := storage.SameVersionRoot(current.Rows, c.Rows) && current.Ends == c.Ends && current.Generation == c.Generation && current.Retire == c.Retire && current.Cutoff >= c.Cutoff && current.Attached == c.Attached && current.IndexCount == c.IndexCount && store.IndexRootsCurrent(c.Indexes)
	if rewritesCatalog && current.Catalog != c.Catalog {
		same = false
	}
	return same
}

// VacuumPass is one detached work pass: the candidate heap, its generation,
// the remapped index roots and, when the relation lost its last index, the
// catalog state to install.
type VacuumPass struct {
	Candidate  storage.VacuumCandidate
	Generation uint64
	Roots      map[ids.ObjectAddress]*storage.IndexRoot
	Catalog    *catalog.State
}

// PrepareVacuumPass runs one work pass over c for store with nothing held:
// inspect, materialize when it removes or freezes a version, bump the
// generation when the heap changes, remap the index roots that need it and
// clear the relation's HasIndexes once no index is attached. phase reports
// each progress phase in order with the total and processed index counts,
// including one update per processed index; it takes Engine.mu itself.
func PrepareVacuumPass(ctx context.Context, c VacuumCapture, store *storage.Relation, phase func(name string, total, processed int64)) (VacuumPass, error) {
	total := int64(len(c.Indexes))
	phase("scanning heap", total, 0)
	candidate, err := storage.InspectVacuum(ctx, c.Rows, c.Ends, c.Frontier, c.Cutoff, c.Retire)
	if err != nil {
		return VacuumPass{}, err
	}
	if candidate.Removed != 0 || candidate.Frozen != 0 {
		phase("vacuuming heap", total, 0)
		candidate, err = storage.MaterializeVacuum(ctx, c.Rows, c.Ends, c.Frontier, c.Cutoff, candidate)
		if err != nil {
			return VacuumPass{}, err
		}
	}
	changed := !storage.SameVersionRoot(candidate.Versions, c.Rows) || candidate.Ends != c.Ends
	pass := VacuumPass{Candidate: candidate, Generation: c.Generation}
	if changed {
		if pass.Generation == ^uint64(0) {
			return VacuumPass{}, fmt.Errorf("stuntgres: storage generation exhausted")
		}
		pass.Generation++
	}
	processed := int64(0)
	indexWork := 0
	if changed {
		if candidate.Positions != nil {
			indexWork = len(c.Indexes)
		} else {
			for _, root := range c.Indexes {
				if root.Hash != nil {
					indexWork++
				}
			}
		}
	}
	if indexWork != 0 {
		phase("vacuuming indexes", total, 0)
		pass.Roots = make(map[ids.ObjectAddress]*storage.IndexRoot, indexWork)
		for address, root := range c.Indexes {
			if candidate.Positions == nil && root.Hash == nil {
				continue
			}
			remapped, err := storage.RemapVacuumIndex(ctx, root, c.Rows, candidate, store, pass.Generation)
			if err != nil {
				return VacuumPass{}, err
			}
			pass.Roots[address] = remapped
			processed++
			phase("vacuuming indexes", total, processed)
		}
	}
	if c.Relation.History.HasIndexes && c.Attached == 0 {
		pass.Catalog = catalog.CloneState(c.Catalog)
		d := *c.Relation
		d.History.HasIndexes = false
		pass.Catalog.SetDescriptor(d.Address(), &d)
	}
	phase("performing final cleanup", total, processed)
	return pass, nil
}

// VacuumReport is the VERBOSE INFO message for relation d of view after a pass
// that produced c.
func VacuumReport(view catalog.Read, d *catalog.Relation, c storage.VacuumCandidate) string {
	ns, _ := view.Namespace(d.Namespace())
	qualified := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name())
	return fmt.Sprintf("vacuuming %s (native logical storage): %d live rows, %d retained dead versions, %d dead versions removed, %d versions frozen", qualified, c.Live, c.Dead, c.Removed, c.Frozen)
}

// DatabaseFrozenXIDLocked is the database frozen-XID bound: cutoff clamped by
// the bound of every heap of state in relations. It reports false while any
// such bound is unknown. Caller holds Engine.mu.
func DatabaseFrozenXIDLocked(state *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, cutoff uint64) (uint64, bool) {
	next := cutoff
	for address, store := range relations {
		d, ok := state.Relation(address)
		if !ok || !VacuumHeap(d) {
			continue
		}
		bound := store.Maintenance.FrozenXID
		if bound == 0 {
			return 0, false
		}
		if bound < next {
			next = bound
		}
	}
	return next, true
}
