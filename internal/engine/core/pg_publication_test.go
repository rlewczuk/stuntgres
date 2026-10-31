package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func publicationInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6104}
	source, ok := auth.Catalog.Get(a).(*catalog.Relation)
	if !ok {
		t.Fatal("missing pg_publication descriptor")
	}
	body(pgcatalog.Input{Context: op.Context(), Owner: auth.Owner, Catalog: auth.Catalog, Roles: auth.Roles, Ref: auth.Ref(a), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}, auth)
}

func publicationProducer(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(6104)
	if !ok || produce == nil {
		t.Fatal("missing pg_publication producer")
	}
	return produce
}
