package engine

import (
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Membership and identity come from the existing immutable catalog. The caller
// caches this enumeration by catalog identity, never by the parent alone.
func enforcingIndexes(view CatalogRead, parent ObjectAddress) []*relationDescriptor {
	var indexes []*relationDescriptor
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && index.sqlIndex.parent == parent && (index.sqlIndex.unique || index.sqlIndex.isExclusion()) {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	return indexes
}

// Index ordering owns comparison; existing column grouping owns normalized
// claims. Reject only the owned cross-input class whose equality differs.
func validateUniqueProviders(parent *relationDescriptor, definition *indexDefinition, raw []parser.IndexKey) error {
	for i, key := range definition.keys {
		position := parser.Position{}
		if len(raw) != 0 {
			position = raw[i].Pos
		}
		if key.derived != nil {
			return insertError("0A000", "unique expression indexes are unsupported", position)
		}
		usage := parent.columns[slices.Index(parent.attributes, key.attribute)].Usage
		if (key.class.Name == "bpchar_ops" || key.class.Name == "bpchar_pattern_ops") && usage.Type.BaseType().ID() != sqltype.BPChar {
			if len(raw) != 0 && raw[i].Opclass != nil {
				position = raw[i].Opclass.Parts[0].Pos
			}
			return insertError("0A000", "unique index operator class is unsupported", position)
		}
		if key.class.Family == "network_ops" {
			if _, err := sqltype.BindIndexOrdering(indexKeyCollation(usage, key), key.class); err != nil {
				return err
			}
			continue
		}
		if _, err := sqltype.BindGrouping(indexKeyCollation(usage, key)); err != nil {
			return err
		}
	}
	return nil
}
