package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// Detached pg_index-shaped values. Analyzed expression nodes are immutable;
// the expression list and all vectors belong to the projection, not descriptors.
type indexMetadata struct {
	IndexRelID, IndRelID                       OID
	IndNatts, IndNKeyAtts                      int16
	IndIsUnique, IndNullsNotDistinct           bool
	IndIsPrimary, IndIsExclusion, IndImmediate bool
	IndIsClustered, IndIsValid, IndCheckXmin   bool
	IndIsReady, IndIsLive, IndIsReplIdent      bool
	IndKey                                     []int16
	IndClass, IndCollation                     []OID
	IndOption                                  []int16
	IndexExpressions                           []BoundExpr
	Predicate                                  BoundExpr
}

type indexesMetadata struct {
	SchemaName, TableName, IndexName string
	Tablespace                       *string
	IndexDef                         string
}

// Both projections use exactly this capture. In particular, deparsing does not
// consult the current engine or filter records through the caller's ACL.
func projectIndexMetadata(ctx context.Context, catalog CatalogRead, ref ObjectRef) (indexMetadata, indexesMetadata, error) {
	var m indexMetadata
	var names indexesMetadata
	if err := ctx.Err(); err != nil {
		return m, names, err
	}
	if catalog.owner == nil || catalog.state == nil || ref.owner != catalog.owner.owner || ref.Epoch != catalog.Epoch() || ref.Address.SubID != 0 {
		return m, names, fmt.Errorf("stuntgres: invalid index metadata capture")
	}
	index, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || index.sqlIndex == nil {
		return m, names, fmt.Errorf("stuntgres: index metadata target is missing")
	}
	sql, err := indexDefinitionSQL(ctx, catalog, index, indexPresentation{})
	if err != nil {
		return m, names, err
	}
	definition := index.sqlIndex
	parent := catalog.state.descriptors[definition.parent].(*relationDescriptor)
	n := len(definition.keys)
	ready, valid, live := definition.publicationState()
	m = indexMetadata{IndexRelID: index.address.ObjectID, IndRelID: parent.address.ObjectID, IndNatts: int16(n + len(definition.include)), IndNKeyAtts: int16(n), IndImmediate: true, IndIsValid: valid, IndIsReady: ready, IndIsLive: live, IndKey: make([]int16, n+len(definition.include)), IndClass: make([]OID, n), IndCollation: make([]OID, n), IndOption: make([]int16, n)}
	m.IndIsUnique = definition.unique
	m.IndNullsNotDistinct = definition.nullsNotDistinct
	for i, key := range definition.keys {
		if err := ctx.Err(); err != nil {
			return indexMetadata{}, indexesMetadata{}, err
		}
		attribute, node, err := indexCatalogKey(parent, key)
		if err != nil {
			return indexMetadata{}, indexesMetadata{}, err
		}
		if node != nil {
			if m.IndexExpressions == nil {
				m.IndexExpressions = make([]BoundExpr, 0, n-i)
			}
			m.IndexExpressions = append(m.IndexExpressions, node)
		} else {
			if attribute < 1 || attribute > 32767 {
				return indexMetadata{}, indexesMetadata{}, fmt.Errorf("stuntgres: index attribute cannot be represented in int2vector")
			}
			m.IndKey[i] = int16(attribute)
		}
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return indexMetadata{}, indexesMetadata{}, err
		}
		m.IndClass[i] = OID(key.class.OID())
		if m.IndClass[i] == 0 {
			return indexMetadata{}, indexesMetadata{}, fmt.Errorf("stuntgres: index class has no released identity")
		}
		m.IndCollation[i] = OID(usage.Collation.ID)
		if definition.method == hashMethod {
			continue
		}
		if key.desc {
			m.IndOption[i] |= 1
		}
		if key.nullsFirst {
			m.IndOption[i] |= 2
		}
	}
	for i, attribute := range definition.include {
		if err := ctx.Err(); err != nil {
			return indexMetadata{}, indexesMetadata{}, err
		}
		if attribute < 1 || attribute > 32767 {
			return indexMetadata{}, indexesMetadata{}, fmt.Errorf("stuntgres: included index attribute cannot be represented in int2vector")
		}
		m.IndKey[n+i] = int16(attribute)
	}
	names = indexesMetadata{SchemaName: catalog.state.descriptors[parent.namespace].Name(), TableName: parent.name, IndexName: index.name, IndexDef: sql}
	return m, names, nil
}

// The enumeration owner for later pg_index and pg_indexes inquiry owners. It
// intentionally registers no SQL source, vector codec, or node-tree list API.
func indexCatalogMetadata(ctx context.Context, catalog CatalogRead) ([]indexMetadata, []indexesMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if catalog.owner == nil || catalog.state == nil {
		return nil, nil, fmt.Errorf("stuntgres: invalid index metadata capture")
	}
	var indexes []*relationDescriptor
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int { return cmp.Compare(a.address.ObjectID, b.address.ObjectID) })
	records := make([]indexMetadata, len(indexes))
	names := make([]indexesMetadata, len(indexes))
	for i, index := range indexes {
		var err error
		records[i], names[i], err = projectIndexMetadata(ctx, catalog, ObjectRef{owner: catalog.owner.owner, Epoch: catalog.Epoch(), Address: index.address})
		if err != nil {
			return nil, nil, err
		}
	}
	return records, names, nil
}
