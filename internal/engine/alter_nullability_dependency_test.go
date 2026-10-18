package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterNullabilityGrowingDependencyClosure(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	run(t, e, "INSERT INTO t VALUES(7,9)")
	run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	table := roleTableRef(t, e, "t")
	view := roleTableRef(t, e, "v")
	old := e.state.descriptors[table.Address]
	originalView := e.state.descriptors[view.Address].(*relationDescriptor)
	store := e.storage[table.Address]
	provider := e.providers[ids.RelationClassID]
	var added ids.ObjectAddress
	e.providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		e.providers[ids.RelationClassID] = provider
		e.mu.Lock()
		oid, err := e.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		rule, err := e.allocateOIDLocked()
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.revisionLocked()
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		next := *originalView
		next.address.ObjectID = oid
		next.name = "late_view"
		next.revision = revision
		definition := *originalView.view
		definition.rule = rule
		next.view = &definition
		next.columns = append([]ColumnDescriptor(nil), originalView.columns...)
		for i := range next.columns {
			next.columns[i].OriginTable = uint32(oid)
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
		added = next.address
	}}
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t DROP b CASCADE, ALTER a SET NOT NULL"), nil)
	if err == nil {
		t.Fatal("stale dependency closure installed")
	}
	if added == (ids.ObjectAddress{}) || e.state.descriptors[added] == nil || e.state.descriptors[table.Address] != old || e.state.descriptors[view.Address] != originalView || e.storage[table.Address] != store {
		t.Fatal("failed list published a closure prefix", err)
	}
	if got := tuples(run(t, e, "SELECT * FROM late_view")); len(got) != 1 || got[0][0] != int64(9) {
		t.Fatal(got)
	}
}
