package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The final installer consumes completed private data. It owns only the final
// cancellation observation, authority check and indivisible publication.
type remintedIndexStorage struct {
	catalog              *catalogState
	baseCatalog          *catalogState
	roots                map[ids.ObjectAddress]*logicalIndexRoot
	nextRow, nextVersion uint64
	baseRow, baseVersion uint64
	frontier             ids.CommitSeq
}

func (e *Engine) prepareForeignIndexStorage(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, snapshot *indexSnapshot) (remintedIndexStorage, error) {
	prepared := remintedIndexStorage{catalog: next, baseCatalog: e.catalog.state, baseRow: e.nextRow, baseVersion: e.nextVersion, frontier: e.commitSeq}
	var err error
	prepared.roots, prepared.nextRow, prepared.nextVersion, err = e.prepareRemintedStorage(ctx, next, storage, snapshot)
	if err == nil {
		next.aclItemImport = nil
	}
	return prepared, err
}

// Imported storage is private to replacement. Exact retained TEMP storage keeps
// its internal lifetime and row identities. Allocator frontiers advance only
// after comparison, root construction and cancellation have all succeeded.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, snapshot *indexSnapshot) (map[ids.ObjectAddress]*logicalIndexRoot, uint64, uint64, error) {
	retained := &catalogState{descriptors: make(map[ids.ObjectAddress]Descriptor)}
	for address, descriptor := range next.Descriptors() {
		namespace, temporary := next.Namespace(descriptor.Namespace())
		if temporary && namespace.Temporary != (ids.SessionToken{}) && descriptor == e.catalog.state.Get(address) {
			retained.setDescriptor(address, descriptor)
			continue
		}
		if index, ok := descriptor.(*relationDescriptor); ok && index.SQLIndex != nil && (!index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live) {
			parent, exists := next.Relation(index.SQLIndex.Parent)
			if exists && parent.Partition != nil && index.SQLIndex.Ready && index.SQLIndex.Live {
				continue
			}
			return nil, 0, 0, fmt.Errorf("stuntgres: replacement cannot import invalid index %s", quoteIdentifier(index.name))
		}
	}
	rowID, versionID := e.nextRow, e.nextVersion
	if snapshot == nil {
		return nil, 0, 0, ids.ErrCatalogReplaced
	}
	positions := make(map[ids.ObjectAddress]map[indexRowIdentity]int, len(storage))
	for address, relation := range storage {
		if descriptor := next.Get(address); descriptor != nil && e.storage[address] == relation {
			if namespace, ok := next.Namespace(descriptor.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
		}
		selected := make(map[indexRowIdentity]int, len(relation.versions))
		positions[address] = selected
		for i := range relation.versions {
			rowID++
			versionID++
			row := &relation.versions[i]
			if err := ctx.Err(); err != nil {
				return nil, 0, 0, err
			}
			identity := indexRowIdentity{row: row.row, version: row.version}
			if _, duplicate := selected[identity]; duplicate {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			selected[identity] = i
			row.row, row.version = ids.RowID(rowID), ids.VersionID(versionID)
			row.base, row.born = 0, e.commitSeq+1
			row.keyChangedAt, row.pendingKeyChange = 0, false
			row.indexAdmission = nil
			row.indexOutputs = nil
		}
	}
	roots, err := snapshot.transport(ctx, next, storage, positions)
	if err != nil {
		return nil, 0, 0, err
	}
	for address, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			continue
		}
		parent, ok := next.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, 0, 0, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		if retained.Get(address) != nil {
			root := e.indexRoots[address]
			if root == nil {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			roots[address] = root
			continue
		}
		root := roots[address]
		if root == nil {
			return nil, 0, 0, ids.ErrCatalogReplaced
		}
		schema := ""
		if index.SQLIndex.Unique || index.SQLIndex.isExclusion() {
			namespace := next.Get(parent.namespace)
			if namespace == nil {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			schema = namespace.Name()
		}
		if err := validateUniqueRoot(ctx, parent, index, root, schema, sqltype.OutputEnvironment{}, nil, true); err != nil {
			return nil, 0, 0, err
		}
	}
	return roots, rowID, versionID, ctx.Err()
}
