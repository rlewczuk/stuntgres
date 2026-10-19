package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The final installer consumes completed private data. It owns only the final
// cancellation observation, authority check and indivisible publication.
type remintedIndexStorage struct {
	catalog              *catalog.State
	baseCatalog          *catalog.State
	roots                map[ids.ObjectAddress]*storage.IndexRoot
	nextRow, nextVersion uint64
	baseRow, baseVersion uint64
	frontier             ids.CommitSeq
}

func (e *Engine) prepareForeignIndexStorage(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *indexSnapshot) (remintedIndexStorage, error) {
	prepared := remintedIndexStorage{catalog: next, baseCatalog: e.catalog.state, baseRow: e.storage.RowFrontier(), baseVersion: e.storage.VersionFrontier(), frontier: e.visibility.CommitSeq()}
	var err error
	prepared.roots, prepared.nextRow, prepared.nextVersion, err = e.prepareRemintedStorage(ctx, next, relations, snapshot)
	if err == nil {
		next.SetACLItemImport(nil)
	}
	return prepared, err
}

// Imported storage is private to replacement. Exact retained TEMP storage keeps
// its internal lifetime and row identities. Allocator frontiers advance only
// after comparison, root construction and cancellation have all succeeded.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *indexSnapshot) (map[ids.ObjectAddress]*storage.IndexRoot, uint64, uint64, error) {
	retained := catalog.NewState(0)
	for address, descriptor := range next.Descriptors() {
		namespace, temporary := next.Namespace(descriptor.Namespace())
		if temporary && namespace.Temporary != (ids.SessionToken{}) && descriptor == e.catalog.state.Get(address) {
			retained.SetDescriptor(address, descriptor)
			continue
		}
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && (!index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live) {
			parent, exists := next.Relation(index.SQLIndex.Parent)
			if exists && parent.Partition != nil && index.SQLIndex.Ready && index.SQLIndex.Live {
				continue
			}
			return nil, 0, 0, fmt.Errorf("stuntgres: replacement cannot import invalid index %s", quoteIdentifier(index.Name()))
		}
	}
	rowID, versionID := e.storage.RowFrontier(), e.storage.VersionFrontier()
	if snapshot == nil {
		return nil, 0, 0, ids.ErrCatalogReplaced
	}
	positions := make(map[ids.ObjectAddress]map[indexRowIdentity]int, len(relations))
	for address, relation := range relations {
		if descriptor := next.Get(address); descriptor != nil && e.storage.Relations[address] == relation {
			if namespace, ok := next.Namespace(descriptor.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
		}
		selected := make(map[indexRowIdentity]int, len(relation.Versions))
		positions[address] = selected
		for i := range relation.Versions {
			rowID++
			versionID++
			row := &relation.Versions[i]
			if err := ctx.Err(); err != nil {
				return nil, 0, 0, err
			}
			identity := indexRowIdentity{row: row.Row, version: row.Version}
			if _, duplicate := selected[identity]; duplicate {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			selected[identity] = i
			row.Row, row.Version = ids.RowID(rowID), ids.VersionID(versionID)
			row.Base, row.Born = 0, e.visibility.CommitSeq()+1
			row.KeyChangedAt, row.PendingKeyChange = 0, false
			row.IndexAdmission = nil
			row.IndexOutputs = nil
		}
	}
	roots, err := snapshot.transport(ctx, next, relations, positions)
	if err != nil {
		return nil, 0, 0, err
	}
	for address, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
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
			root := e.storage.IndexRoots[address]
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
		if index.SQLIndex.Unique || index.SQLIndex.IsExclusion() {
			namespace := next.Get(parent.Namespace())
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
