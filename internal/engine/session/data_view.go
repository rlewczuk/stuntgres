package session

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// DataViewInput is one statement's data-view capture input. The root fills it
// under Engine.mu after its epoch checks and snapshot step, and passes it by
// pointer; nothing here retains it or takes a lock.
type DataViewInput struct {
	Published *catalog.State // the engine's published catalog
	Owner     *catalog.Owner // the published catalog's owner
	Store     *storage.Store // the engine's published storage
	Journal   *Journal       // the transaction's journal, or nil
	Read      catalog.Read   // Journal's private read; Capture only

	Base          *visibility.Data // a dynamic extension's outer view, or nil
	Latest        bool             // a latest-DDL scan, not the statement snapshot
	Stable        bool             // the owner's isolation is transaction-stable
	RetainDynamic bool             // keep every root for runtime-named pgrowlocks targets
	Tx            ids.TxRef
	Command       ids.CommandID
	Frontier      ids.CommitSeq // the capture's commit frontier
	CommitSeq     ids.CommitSeq // the engine's current commit sequence

	Roots      map[ids.ObjectAddress]visibility.Root // the stable owner's retained roots
	Target     visibility.Root                       // the first admitted RC target root
	Targets    map[ids.ObjectAddress]visibility.Root // further admitted RC target roots
	OwnWrites  map[ids.ObjectAddress][]storage.RowVersion
	OwnDeletes map[ids.ObjectAddress]*visibility.PrivateDeletes

	// SameLineage is view.SameMaterializedLineage and SameRowShape is
	// index.SameRelationRowShape; session imports neither package.
	SameLineage  func(old, current *catalog.Relation, a, b *storage.Relation) bool
	SameRowShape func(a, b *catalog.Relation) bool
}

// ValidateView checks that existing, the statement's already captured view,
// still covers refs: each relation was captured and its storage incarnation
// is current or continues the same materialized lineage.
func (in *DataViewInput) ValidateView(existing *visibility.View, refs []ids.ObjectRef) error {
	data := existing.Load()
	if data == nil {
		return errors.New("stuntgres: visibility view is released")
	}
	for _, ref := range refs {
		if _, ok := data.Committed[ref.Address]; !ok {
			return errors.New("stuntgres: relation is outside statement data view")
		}
		captured := data.GenerationAt(ref.Address)
		current := PrivateStorage(in.Store, in.Journal, ref.Address)
		if captured != current {
			oldRelation, _ := data.Catalog.Relation(ref.Address)
			liveRelation, _ := in.Journal.PrivateRead(in.Owner, in.Published).Relation(ref.Address)
			if !in.SameLineage(oldRelation, liveRelation, captured, current) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	return nil
}

// Capture builds a new immutable view of refs. Every ref has private storage
// (the root checked it).
func (in *DataViewInput) Capture(refs []ids.ObjectRef) (*visibility.View, error) {
	data := &visibility.Data{
		Tx: in.Tx, Command: in.Command, Frontier: in.Frontier,
		Catalog:   in.Read,
		Committed: make(map[ids.ObjectAddress][]storage.RowVersion, len(refs)),
	}
	if in.Base != nil {
		data.DynamicRoots = in.Base.DynamicRoots
	} else if in.RetainDynamic {
		data.DynamicRoots = in.dynamicRoots()
	}
	if len(refs) > 1 {
		data.Generations = make(map[ids.ObjectAddress]visibility.Generation, len(refs))
	}
	for _, ref := range refs {
		if err := in.captureRoot(data, ref.Address); err != nil {
			return nil, err
		}
	}
	data.Deleted = in.selectDeletes(refs)
	return visibility.NewView(data), nil
}

// dynamicRoots pairs every published and private storage relation with its
// private descriptor.
func (in *DataViewInput) dynamicRoots() map[ids.ObjectAddress]visibility.Root {
	roots := make(map[ids.ObjectAddress]visibility.Root, len(in.Store.Relations))
	add := func(address ids.ObjectAddress) {
		stored := PrivateStorage(in.Store, in.Journal, address)
		if relation, ok := in.Read.Relation(address); ok && stored != nil {
			roots[address] = visibility.Root{Versions: stored.Versions, Ends: stored.Ends, Storage: stored, Relation: relation}
		}
	}
	for address := range in.Store.Relations {
		add(address)
	}
	if in.Journal != nil {
		for address := range in.Journal.Storage {
			add(address)
		}
	}
	return roots
}

func (in *DataViewInput) targetRoot(address ids.ObjectAddress) (visibility.Root, bool) {
	if in.Target.Relation != nil && in.Target.Relation.Address() == address {
		return in.Target, true
	}
	root, exists := in.Targets[address]
	return root, exists
}

// rootCapture is the committed rows, end markers and birth-filter need that
// captureRoot selects for one relation.
type rootCapture struct {
	versions         []storage.RowVersion
	ends             *storage.DeletionRoot
	needsBirthFilter bool
}

// captureRoot records address's committed rows, generation, ends and own
// writes in data.
func (in *DataViewInput) captureRoot(data *visibility.Data, address ids.ObjectAddress) error {
	stored := PrivateStorage(in.Store, in.Journal, address)
	// Roots obtained after utility entry (or during dynamic extension)
	// can contain commits newer than the already active command frontier.
	c := rootCapture{stored.Versions, stored.Ends, !in.Latest && !in.Stable && in.Frontier < in.CommitSeq}
	var rewrite StorageChange
	if in.Journal != nil {
		rewrite = in.Journal.Storage[address]
	}
	if !in.Latest && in.Base == nil && !in.Stable && !rewrite.Rewritten {
		if root, exists := in.targetRoot(address); exists {
			current, ok := in.Read.Relation(address)
			if !ok || root.Storage != stored || !in.SameRowShape(root.Relation, current) {
				return ids.ErrCatalogReplaced
			}
			c.versions, c.ends = root.Versions, root.Ends
			c.needsBirthFilter = false
		}
	}
	if in.Base != nil && (!in.Stable || rewrite.Rewritten) {
		root, ok := in.Base.DynamicRoots[address]
		if !ok || root.Storage != stored {
			return ids.ErrCatalogReplaced
		}
		c.versions, c.ends = root.Versions, root.Ends
	}
	if !in.Latest && in.Stable && !rewrite.Rewritten {
		if err := in.stableRoot(address, &c); err != nil {
			return err
		}
	}
	if !in.Latest && rewrite.Rewritten && in.Stable {
		if data.RewrittenFrontiers == nil {
			data.RewrittenFrontiers = make(map[ids.ObjectAddress]ids.CommitSeq)
		}
		data.RewrittenFrontiers[address] = rewrite.Frontier
	}
	data.Committed[address] = c.versions
	if data.Generations != nil {
		data.Generations[address] = visibility.Generation{Storage: stored, NeedsBirthFilter: c.needsBirthFilter}
	} else {
		data.SingleGeneration = stored
		data.SingleNeedsBirthFilter = c.needsBirthFilter
	}
	if c.ends != nil {
		if data.Ends == nil {
			data.Ends = make(map[ids.ObjectAddress]*storage.DeletionRoot)
		}
		data.Ends[address] = c.ends
	}
	if rows := in.OwnWrites[address]; len(rows) != 0 {
		if data.Own == nil {
			data.Own = make(map[ids.ObjectAddress][]storage.RowVersion)
		}
		data.Own[address] = rows
	}
	return nil
}

// stableRoot selects a stable owner's retained root for address over the
// published relation and storage. A relation created after the snapshot has
// no root and reads no committed rows.
func (in *DataViewInput) stableRoot(address ids.ObjectAddress, c *rootCapture) error {
	root, exists := in.Roots[address]
	if !exists {
		c.versions, c.ends = nil, nil
		return nil
	}
	current, ok := in.Published.Relation(address)
	live := in.Store.Relations[address]
	if !ok || !in.SameRowShape(root.Relation, current) {
		return ids.ErrCatalogReplaced
	}
	if root.Storage == live {
		c.versions, c.ends = root.Versions, root.Ends
	} else if in.SameLineage(root.Relation, current, root.Storage, live) {
		// Concurrent MV publication retains the actual RR root and
		// its end markers, not the new incarnation with a birth filter.
		c.versions, c.ends = root.Versions, root.Ends
	} else {
		if !root.Storage.SameTruncateLineage(live) {
			return ids.ErrCatalogReplaced
		}
		c.needsBirthFilter = true
	}
	// Proven TRUNCATE retirement keeps the original frontier but
	// uses the current incarnation, never the retained old rows.
	if in.Journal != nil {
		if shape, changed := in.Journal.Storage[address]; changed && shape.HasSnapshot {
			c.versions = shape.Snapshot
			c.needsBirthFilter = false // Projected frozen snapshots retain their birth bound.
		}
	}
	return nil
}

// selectDeletes returns the statement's private deletes of refs, reusing the
// statement's own map when it holds exactly those roots.
func (in *DataViewInput) selectDeletes(refs []ids.ObjectRef) map[ids.ObjectAddress]*visibility.PrivateDeletes {
	if len(in.OwnDeletes) == 0 {
		return nil
	}
	selected := 0
	for _, ref := range refs {
		if in.OwnDeletes[ref.Address] != nil {
			selected++
		}
	}
	if selected == len(in.OwnDeletes) {
		// Reuse an already exact immutable root set, including the usual
		// single-target case, rather than allocating a redundant map.
		return in.OwnDeletes
	}
	if selected == 0 {
		return nil
	}
	deleted := make(map[ids.ObjectAddress]*visibility.PrivateDeletes, selected)
	for _, ref := range refs {
		if root := in.OwnDeletes[ref.Address]; root != nil {
			deleted[ref.Address] = root
		}
	}
	return deleted
}
