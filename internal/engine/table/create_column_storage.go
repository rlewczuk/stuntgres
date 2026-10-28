package table

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
)

// materializeCreateColumnStorage installs the checked declaration policies once,
// before row types, inheritance recipes and index constructors see the private
// relation. No source map is retained and only nondefault fields need entries.
func materializeCreateColumnStorage(destination *catalog.Relation, b analyze.Binding) {
	for slot, column := range destination.Columns {
		properties, inherited := b.Create.InheritedStorage[column.Name]
		if !inherited {
			declaration := b.Create.Declarations[slot]
			properties = catalog.ColumnStorage{Storage: declaration.Storage, Compression: declaration.Compression}
		}
		if properties.Storage == column.Usage.Type.AttributeFacts().Storage {
			properties.Storage = ""
		}
		if properties != (catalog.ColumnStorage{}) {
			if destination.ColumnMeta.Storage == nil {
				destination.ColumnMeta.Storage = make(map[int32]catalog.ColumnStorage)
			}
			destination.ColumnMeta.Storage[destination.Attributes[slot]] = properties
		}
	}
}
