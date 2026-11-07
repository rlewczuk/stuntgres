package index

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
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
// claims. Every key resolves usage through KeyUsage, so derived keys use their
// expression result usage while opclass, cross-input class, comparator and
// grouping admission apply identically; reject only the owned cross-input class
// whose equality differs.
func ValidateUniqueProviders(view catalog.Read, parent *catalog.Relation, definition *catalog.IndexDefinition, raw []parser.IndexKey) error {
	for i, key := range definition.Keys {
		position := parser.Position{}
		if len(raw) != 0 {
			position = raw[i].Pos
		}
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return err
		}
		class, exists := view.OperatorClass(ids.OID(key.Class.OID()))
		if !exists {
			return insertError("42704", "index operator class does not exist", position)
		}
		if class.InputType == ids.OID(sqltype.BPChar) && usage.Type.BaseType().ID() != sqltype.BPChar {
			if len(raw) != 0 && raw[i].Opclass != nil {
				position = raw[i].Opclass.Parts[0].Pos
			}
			return insertError("0A000", "unique index operator class is unsupported", position)
		}
		if usage.Type.BaseType().ID() == sqltype.Inet || usage.Type.BaseType().ID() == sqltype.Cidr {
			if _, err := routine.BindIndexOrdering(view, usage, key.Class); err != nil {
				return err
			}
			continue
		}
		if _, err := sqltype.BindGrouping(usage); err != nil {
			return err
		}
	}
	return nil
}
