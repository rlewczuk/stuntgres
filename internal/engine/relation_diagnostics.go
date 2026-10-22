package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
)

func relationDiagnosticName(auth AuthorizationView, relation *catalog.Relation) string {
	path, _ := referencePath(auth)
	return stored.RelationDiagnosticNameAt(auth.Catalog, relation, path)
}
