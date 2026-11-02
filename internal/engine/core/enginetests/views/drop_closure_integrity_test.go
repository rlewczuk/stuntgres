package views_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func TestDropClosureRetainedQueryAuthority(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "projected_attribute", true: "retired_provider"}[retired], func(t *testing.T) {
			e := engine.New()
			for _, sql := range []string{
				"CREATE DOMAIN closure_integrity_domain AS integer",
				"CREATE TABLE closure_integrity_source(v closure_integrity_domain)",
				"CREATE VIEW closure_integrity_view AS SELECT v FROM closure_integrity_source",
			} {
				enginetest.Run(t, e, sql)
			}
			base := e.CatalogRead()
			source := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_source").Address)
			dependent := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_view").Address)
			domain := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[0].Usage.Type.ID())}
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if retired {
				if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), source.Address())); err != nil {
					t.Fatal(err)
				}
			} else {
				projected := *source
				projected.Columns, projected.Attributes = nil, nil
				if err := stage.Replace(&projected); err != nil {
					t.Fatal(err)
				}
			}
			reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
			before := stage.RemovalCount()
			passive, err := view.PassiveTypeDependency(reader, dependent, domain, false)
			if err != nil || !passive {
				t.Fatal("retained passive query lost entering authority", passive, err)
			}
			if stage.RemovalCount() != before || base.MustRelation(source.Address()) != source {
				t.Fatal("classification mutated retirement or original provider")
			}
		})
	}
}
