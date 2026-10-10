package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Imported storage is private to replacement. Exact retained TEMP storage keeps
// its internal lifetime and row identities. Allocator frontiers advance only
// after comparison, root construction and cancellation have all succeeded.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage) (map[ObjectAddress]*logicalIndexRoot, uint64, uint64, error) {
	retained := &catalogState{descriptors: make(map[ObjectAddress]Descriptor)}
	for address, descriptor := range next.descriptors {
		namespace, temporary := next.descriptors[descriptor.Namespace()].(*namespaceDescriptor)
		if temporary && namespace.temporary != (SessionToken{}) && descriptor == e.state.descriptors[address] {
			retained.descriptors[address] = descriptor
			continue
		}
		if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil && (!index.sqlIndex.ready || !index.sqlIndex.valid || !index.sqlIndex.live) {
			return nil, 0, 0, fmt.Errorf("stuntgres: replacement cannot import invalid index %s", quoteIdentifier(index.name))
		}
	}
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
			row.indexAdmission = nil
		}
	}
	var retainedRoots map[ObjectAddress]*logicalIndexRoot
	for a, root := range e.indexRoots {
		if retained.descriptors[a] == nil {
			continue
		}
		if retainedRoots == nil {
			retainedRoots = make(map[ObjectAddress]*logicalIndexRoot)
		}
		retainedRoots[a] = root
	}
	roots, err := e.prepareIndexCatalog(ctx, retained, next, storage, retainedRoots, nil, nil, sqltype.OutputEnvironment{}, nil)
	return roots, rowID, versionID, err
}
