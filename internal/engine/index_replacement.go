package engine

import "context"

// Imported storage is private to replacement. Exact retained TEMP storage keeps
// its internal lifetime and row identities. Allocator frontiers advance only
// after comparison, root construction and cancellation have all succeeded.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage) (map[ObjectAddress]*logicalIndexRoot, uint64, uint64, error) {
	rowID, versionID := e.nextRow, e.nextVersion
	for address, relation := range storage {
		if descriptor := next.descriptors[address]; descriptor != nil && e.storage[address] == relation {
			if namespace, ok := next.descriptors[descriptor.Namespace()].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
				continue
			}
		}
		for i := range relation.versions {
			rowID++
			versionID++
			row := &relation.versions[i]
			row.row, row.version = RowID(rowID), VersionID(versionID)
			row.base, row.born = 0, e.commitSeq+1
			row.keyChangedAt, row.pendingKeyChange = 0, false
		}
	}
	roots, err := e.prepareIndexCatalog(ctx, &catalogState{}, next, storage, nil, nil, nil)
	return roots, rowID, versionID, err
}
