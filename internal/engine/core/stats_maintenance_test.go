package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func statsPrivateMaintenanceKey(t *testing.T, e *core.Engine, s *session.Session, journal *session.Journal, name string) statistics.Key {
	t.Helper()
	if journal != nil {
		for a, d := range journal.Descriptors {
			if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
				return statistics.Key{Object: e.Catalog.Domain.Ref(s.Transactions.Ref.Epoch(), a), Category: statistics.Relation}
			}
		}
	}
	t.Fatal("missing actual private relation", name)
	return statistics.Key{}
}
