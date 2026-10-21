package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Membership and identity come from the existing immutable catalog. The caller
// caches this enumeration by catalog identity, never by the parent alone.
func enforcingIndexes(view catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	var indexes []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent && (index.SQLIndex.Unique || index.SQLIndex.IsExclusion()) {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return indexes
}

// Index ordering owns comparison; existing column grouping owns normalized
// claims. Reject only the owned cross-input class whose equality differs.
func ValidateUniqueProviders(parent *catalog.Relation, definition *catalog.IndexDefinition, raw []parser.IndexKey) error {
	for i, key := range definition.Keys {
		position := parser.Position{}
		if len(raw) != 0 {
			position = raw[i].Pos
		}
		if key.Derived != nil {
			return insertError("0A000", "unique expression indexes are unsupported", position)
		}
		usage := parent.Columns[slices.Index(parent.Attributes, key.Attribute)].Usage
		if (key.Class.Name == "bpchar_ops" || key.Class.Name == "bpchar_pattern_ops") && usage.Type.BaseType().ID() != sqltype.BPChar {
			if len(raw) != 0 && raw[i].Opclass != nil {
				position = raw[i].Opclass.Parts[0].Pos
			}
			return insertError("0A000", "unique index operator class is unsupported", position)
		}
		if key.Class.Family == "network_ops" {
			if _, err := sqltype.BindIndexOrdering(KeyCollation(usage, key), key.Class); err != nil {
				return err
			}
			continue
		}
		if _, err := sqltype.BindGrouping(KeyCollation(usage, key)); err != nil {
			return err
		}
	}
	return nil
}
