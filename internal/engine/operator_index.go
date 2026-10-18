package engine

import (
	"stuntgres/internal/engine/ids"
)

var seededOperatorNames = func() *operatorNameIndex {
	index := &operatorNameIndex{Buckets: make(map[operatorNameKey][]ids.ObjectAddress)}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		key := operatorNameKey{namespace, row.name}
		index.Buckets[key] = append(index.Buckets[key], ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
	}
	return index
}()
