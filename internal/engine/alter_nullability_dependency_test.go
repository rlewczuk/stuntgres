package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestAlterNullabilityGrowingDependencyClosure(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	run(t, e, "INSERT INTO t VALUES(7,9)")
	run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	table := roleTableRef(t, e, "t")
	view := roleTableRef(t, e, "v")
	old := e.catalog.State.Get(table.Address)
	originalView := e.catalog.State.MustRelation(view.Address)
	store := e.storage.Relations[table.Address]
	provider := e.catalog.Providers[ids.RelationClassID]
	var added ids.ObjectAddress
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		e.catalog.Providers[ids.RelationClassID] = provider
		e.mu.Lock()
		oid, err := e.catalog.AllocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		rule, err := e.catalog.AllocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.catalog.RevisionLocked()
		e.mu.Unlock()
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
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: table.Epoch}); err != nil {
			t.Fatal(err)
		}
		added = next.Address()
	}}
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t DROP b CASCADE, ALTER a SET NOT NULL"), nil)
	if err == nil {
		t.Fatal("stale dependency closure installed")
	}
	if added == (ids.ObjectAddress{}) || e.catalog.State.Get(added) == nil || e.catalog.State.Get(table.Address) != old || e.catalog.State.Get(view.Address) != originalView || e.storage.Relations[table.Address] != store {
		t.Fatal("failed list published a closure prefix", err)
	}
	if got := tuples(run(t, e, "SELECT * FROM late_view")); len(got) != 1 || got[0][0] != int64(9) {
		t.Fatal(got)
	}
}
