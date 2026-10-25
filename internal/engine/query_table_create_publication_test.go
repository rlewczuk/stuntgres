package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func queryCreateStage(t *testing.T, e *core.Engine, name string, width int) (*core.CatalogStage, *catalog.Relation) {
	t.Helper()
	columns := make([]catalog.Column, width)
	for i := range columns {
		columns[i] = catalog.Column{Name: "i", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}
	}
	e.Mu.Lock()
	d, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), columns, nil)
	stage := e.BeginCatalogStage(e.Catalog.Current())
	if err == nil {
		err = stage.Create(d)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage, d
}

func queryCreateTypes(e *core.Engine) map[ids.ObjectAddress]catalog.Descriptor {
	types := make(map[ids.ObjectAddress]catalog.Descriptor)
	for address, descriptor := range e.CatalogRead().Descriptors() {
		if address.ClassID == ids.TypeClassID {
			types[address] = descriptor
		}
	}
	return types
}

func queryCreateAssertTypes(t *testing.T, e *core.Engine, want map[ids.ObjectAddress]catalog.Descriptor) {
	t.Helper()
	got := queryCreateTypes(e)
	if len(got) != len(want) {
		t.Fatal("type registry leaked a definition", len(got), len(want))
	}
	for address, descriptor := range want {
		if got[address] != descriptor {
			t.Fatal("type generation changed", address)
		}
	}
}

func queryCreateAssertAbsent(t *testing.T, e *core.Engine, d *catalog.Relation) {
	t.Helper()
	view := e.CatalogRead()
	if _, ok := view.Descriptor(d.Address()); ok || e.Storage.Relations[d.Address()] != nil {
		t.Fatal("partial descriptor/storage publication")
	}
	for _, class := range []ids.OID{ids.RelationClassID, ids.TypeClassID} {
		if len(view.Lookup(ids.NameKey{ClassID: class, Namespace: d.Namespace(), Name: d.Name()})) != 0 {
			t.Fatal("partial name/type publication", class)
		}
	}
}

func queryCreatePublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err != nil {
		t.Fatal(err)
	}
}

type queryCreateProbeProvider struct {
	catalog.Provider
	e     *core.Engine
	t     *testing.T
	check func() error
}

func (p queryCreateProbeProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	if !p.e.Mu.TryLock() {
		p.t.Fatal("definition callback under Engine.mu")
	}
	p.e.Mu.Unlock()
	if err := p.Provider.Validate(view, d); err != nil {
		return err
	}
	return p.check()
}
