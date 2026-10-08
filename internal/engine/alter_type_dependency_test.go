package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

func TestAlterTypeGrowingDependencyClosure(t *testing.T) {
	for _, action := range []string{"ALTER c TYPE VARCHAR(8)", "ALTER c TYPE VARCHAR(5)", `ALTER c TYPE VARCHAR(5) COLLATE "C"`} {
		t.Run(action, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(c VARCHAR(5))")
			run(t, e, "INSERT INTO t VALUES('seven')")
			run(t, e, "CREATE VIEW template AS SELECT c FROM t")
			table := roleTableRef(t, e, "t")
			view := roleTableRef(t, e, "template")
			old, store := e.state.descriptors[table.Address], e.storage[table.Address]
			originalView := e.state.descriptors[view.Address].(*relationDescriptor)
			run(t, e, "DROP VIEW template")
			provider := e.providers[RelationClassID]
			var added ObjectAddress
			e.providers[RelationClassID] = alterDropProvider{provider, func() {
				e.providers[RelationClassID] = provider
				e.mu.Lock()
				oid, allocationErr := e.allocateOIDLocked()
				if allocationErr != nil {
					e.mu.Unlock()
					t.Fatal(allocationErr)
				}
				rule, allocationErr := e.allocateOIDLocked()
				if allocationErr != nil {
					e.mu.Unlock()
					t.Fatal(allocationErr)
				}
				revision, allocationErr := e.revisionLocked()
				e.mu.Unlock()
				if allocationErr != nil {
					t.Fatal(allocationErr)
				}
				next := *originalView
				next.address.ObjectID, next.name, next.revision = oid, "late_view", revision
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
			_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t "+action), nil)
			var conflict CatalogConflict
			if !errors.As(err, &conflict) {
				t.Fatal("expected dependency publication conflict", err)
			}
			if added == (ObjectAddress{}) || e.state.descriptors[added] == nil || e.state.descriptors[table.Address] != old || e.storage[table.Address] != store {
				t.Fatal("failed TYPE published a prefix", err)
			}
			if got := tuples(run(t, e, "SELECT * FROM late_view")); !reflect.DeepEqual(got, [][]driver.Value{{"seven"}}) {
				t.Fatal(got)
			}
			run(t, e, "DROP VIEW late_view")
			run(t, e, "ALTER TABLE t "+action)
			if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{"seven"}}) {
				t.Fatal(got)
			}
		})
	}
}
