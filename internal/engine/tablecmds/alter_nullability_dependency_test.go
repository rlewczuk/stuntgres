package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestAlterNullabilityGrowingDependencyClosure(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(7,9)")
	enginetest.Run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	table := enginetest.RoleTableRef(t, e, "t")
	view := enginetest.RoleTableRef(t, e, "v")
	old := e.Catalog.State.Get(table.Address)
	originalView := e.Catalog.State.MustRelation(view.Address)
	store := e.Storage.Relations[table.Address]
	provider := e.Catalog.Providers[ids.RelationClassID]
	var added ids.ObjectAddress
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		e.Catalog.Providers[ids.RelationClassID] = provider
		e.Mu.Lock()
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		rule, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		next := *originalView
		next.ObjectAddress.ObjectID = oid
		next.ObjectName = "late_view"
		next.ObjectRevision = revision
		next.View = stored.ViewOf(originalView).With(func(p *stored.ViewParts) { p.Rule = rule })
		next.Columns = append([]catalog.Column(nil), originalView.Columns...)
		for i := range next.Columns {
			next.Columns[i].OriginTable = uint32(oid)
		}
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		if err := stage.Create(&next); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: table.Epoch}); err != nil {
			t.Fatal(err)
		}
		added = next.Address()
	}}
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t DROP b CASCADE, ALTER a SET NOT NULL"), nil)
	if err == nil {
		t.Fatal("stale dependency closure installed")
	}
	if added == (ids.ObjectAddress{}) || e.Catalog.State.Get(added) == nil || e.Catalog.State.Get(table.Address) != old || e.Catalog.State.Get(view.Address) != originalView || e.Storage.Relations[table.Address] != store {
		t.Fatal("failed list published a closure prefix", err)
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM late_view")); len(got) != 1 || got[0][0] != int64(9) {
		t.Fatal(got)
	}
}
