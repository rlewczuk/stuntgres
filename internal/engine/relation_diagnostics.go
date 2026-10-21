package engine

import (
	"stuntgres/internal/engine/catalog"
)

func relationDiagnosticName(auth AuthorizationView, relation *catalog.Relation) string {
	path, _ := auth.engine.referencePath(auth)
	return RelationDiagnosticNameAt(auth.Catalog, relation, path)
}
