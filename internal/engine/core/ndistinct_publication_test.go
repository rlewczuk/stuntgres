package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/storage"
)

func ndistinctKey(t *testing.T, e *core.Engine, name string) storage.ExtendedStatisticsKey {
	t.Helper()
	for _, raw := range e.CatalogRead().Descriptors() {
		if d, ok := raw.(*catalog.Statistics); ok && d.Name() == name {
			return storage.ExtendedStatisticsKey{Definition: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())}
		}
	}
	t.Fatal("missing definition", name)
	return storage.ExtendedStatisticsKey{}
}
