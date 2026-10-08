package engine

import "context"

// Storage is private to replacement. Allocator frontiers advance only after
// comparison, root construction and cancellation have all succeeded.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage) (map[ObjectAddress]*logicalIndexRoot, uint64, uint64, error) {
	rowID, versionID := e.nextRow, e.nextVersion
	for _, relation := range storage {
		for i := range relation.versions {
			rowID++
			versionID++
			row := &relation.versions[i]
			row.row, row.version = RowID(rowID), VersionID(versionID)
			row.base, row.born = 0, e.commitSeq+1
		}
	}
	roots, err := prepareIndexCatalog(ctx, &catalogState{}, next, storage, nil, nil)
	return roots, rowID, versionID, err
}
