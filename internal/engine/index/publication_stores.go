package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// CandidatesMatch reports whether every concurrent root candidate still
// matches its index and the store the publication builds it over. Each index
// is the staged descriptor, else the current one; storeOf picks the parent's
// store. A missing or non-index relation does not match. It takes no lock;
// the root calls it under Engine.mu and turns false into a catalog.Conflict.
func CandidatesMatch(candidates map[ids.ObjectAddress]RootCandidate, staged *catalog.Candidate, current *catalog.State, storeOf func(parent ids.ObjectAddress) *storage.Relation) bool {
	for a, candidate := range candidates {
		idx, ok := staged.Get(a).(*catalog.Relation)
		if !ok {
			idx, ok = current.Relation(a)
		}
		if !ok || idx.SQLIndex == nil {
			return false
		}
		if !candidate.MatchesIndex(idx, storeOf(idx.SQLIndex.Parent)) {
			return false
		}
	}
	return true
}

// CaptureStagedIndexStores is CaptureIndexStores over current plus a copy of
// the committed parent store of every staged index, so publication fences
// every index build on its captured store.
func CaptureStagedIndexStores(relations map[ids.ObjectAddress]*storage.Relation, current *catalog.State, staged *catalog.Candidate) map[ids.ObjectAddress]*storage.Relation {
	captured := CaptureIndexStores(relations, current)
	for _, d := range staged.Descriptors() {
		idx, ok := d.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			continue
		}
		if captured == nil {
			captured = make(map[ids.ObjectAddress]*storage.Relation)
		}
		a := idx.SQLIndex.Parent
		if captured[a] == nil && relations[a] != nil {
			copy := *relations[a]
			copy.IndexSource = relations[a]
			captured[a] = &copy
		}
	}
	return captured
}

// PublicationStores are the inputs of a catalog publication's index build.
type PublicationStores struct {
	// Build maps each parent to the store its indexes are built from.
	Build map[ids.ObjectAddress]*storage.Relation
	// Rebuild marks parents whose heap is replaced, so their indexes rebuild.
	Rebuild map[ids.ObjectAddress]bool
	// Sources are the predecessor roots with the candidates' roots over them.
	Sources map[ids.ObjectAddress]*storage.IndexRoot
}

// PublicationBuildStores derives the build stores, the rebuild set and the
// source roots from the captured stores. Heap replacements and a refreshed
// store replace their captures and rebuild; a new table or materialized view
// staged with its indexes builds from its candidate store in relations, while
// captured keeps committed absence for the fence. Unchanged inputs are
// returned as they are, not copied.
func PublicationBuildStores(captured map[ids.ObjectAddress]*storage.Relation, p storage.Publication, staged *catalog.Candidate, current *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, predecessors map[ids.ObjectAddress]*storage.IndexRoot, candidates map[ids.ObjectAddress]RootCandidate) PublicationStores {
	out := PublicationStores{Build: captured, Sources: predecessors}
	copied := false
	if len(captured) > 0 && (len(p.Shapes) > 0 || p.Refresh != nil) {
		out.Build = make(map[ids.ObjectAddress]*storage.Relation, len(captured))
		for a, store := range captured {
			out.Build[a] = store
		}
		copied = true
		out.Rebuild = make(map[ids.ObjectAddress]bool, len(p.Shapes))
		for a, shape := range p.Shapes {
			out.Build[a] = shape.Replacement
			out.Rebuild[a] = true
		}
		if p.Refresh != nil {
			out.Build[p.Refreshed] = p.Refresh
			out.Rebuild[p.Refreshed] = true
		}
	}
	for _, d := range staged.Descriptors() {
		idx, ok := d.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			continue
		}
		a := idx.SQLIndex.Parent
		parent, isStaged := staged.Get(a).(*catalog.Relation)
		if !isStaged || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation) || current.Get(a) != nil {
			continue
		}
		if !copied {
			out.Build = make(map[ids.ObjectAddress]*storage.Relation, len(captured)+1)
			for address, store := range captured {
				out.Build[address] = store
			}
			copied = true
		}
		out.Build[a] = relations[a]
	}
	if len(candidates) != 0 {
		out.Sources = make(map[ids.ObjectAddress]*storage.IndexRoot, len(predecessors)+len(candidates))
		for a, root := range predecessors {
			out.Sources[a] = root
		}
		for a, candidate := range candidates {
			out.Sources[a] = candidate.Root
		}
	}
	return out
}
