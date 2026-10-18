package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var seededOperatorNames = func() *catalog.OperatorNameIndex {
	index := &catalog.OperatorNameIndex{Buckets: make(map[catalog.OperatorNameKey][]ids.ObjectAddress)}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		key := catalog.OperatorNameKey{namespace, row.name}
		index.Buckets[key] = append(index.Buckets[key], ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
	}
	return index
}()
