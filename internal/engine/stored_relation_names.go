package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func relationVisibleAt(catalog catalog.Read, relation *catalog.Relation, path []ids.ObjectAddress) bool {
	for _, ns := range path {
		if a, ok := catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: relation.Name()}); ok {
			return a == relation.Address()
		}
	}
	return false
}

func RelationDiagnosticNameAt(catalogView catalog.Read, relation *catalog.Relation, path []ids.ObjectAddress) string {
	name := catalog.DependencyIdentifier(relation.Name())
	if relationVisibleAt(catalogView, relation, path) {
		return name
	}
	return catalog.DependencyIdentifier(catalogView.Get(relation.Namespace()).Name()) + "." + name
}
