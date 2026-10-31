package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func ndistinctCoreFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE nd_owner(a INT,b INT,c INT)", "CREATE STATISTICS nd_a(ndistinct) ON a,b FROM nd_owner", "CREATE STATISTICS nd_b(ndistinct) ON a,c FROM nd_owner", "INSERT INTO nd_owner VALUES(1,1,1),(2,2,2)", "ANALYZE nd_owner"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := enginetest.VacuumStore(t, e, "nd_owner")
	return e, s, ref
}

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
