package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func analyzeTemporaryRelation(t *testing.T, e *core.Engine, s *session.Session, name string) *catalog.Relation {
	t.Helper()
	view := e.CatalogRead()
	for _, descriptor := range view.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == name && view.MustNamespace(relation.Namespace()).Temporary == s.SessionToken {
			return relation
		}
	}
	t.Fatal("missing actual session TEMP relation", name)
	return nil
}
