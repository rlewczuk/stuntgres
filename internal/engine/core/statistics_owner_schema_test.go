package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestStatisticsAlterPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "source_replaced", "namespace_replaced", "destination_collision"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := statisticsUnitEngine(t)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			initial, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
			if err != nil {
				t.Fatal(err)
			}
			old := statisticsUnitDescriptor(t, initial)
			enginetest.PublishOperationStage(t, e, op, initial)
			e.DiscardCatalogStage(initial)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mutation, finish, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(mutation)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, mutation.Session)
			err = ddl.StageRenameStatistics(core.DDLHost{E: e, Op: mutation, CatalogStage: stage}, auth, &parser.AlterStatisticsRename{Name: configName(old.Name()), NewName: parser.Identifier{Text: "statistics_new"}})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			} else {
				e.Mu.Lock()
				state := catalog.CloneState(e.Catalog.State)
				switch mode {
				case "source_replaced":
					n := *old
					n.ObjectRevision = old.Revision() + 1
					n.ObjectAddress.ObjectID += 10000
					state.RemoveDescriptor(old.Address())
					state.SetDescriptor(n.Address(), &n)
					state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				case "namespace_replaced":
					ns := *stage.Base.MustNamespace(old.Namespace())
					ns.ObjectRevision = ns.Revision() + 1
					ns.ObjectAddress.ObjectID += 10000
					state.RemoveDescriptor(old.Namespace())
					state.SetDescriptor(ns.Address(), &ns)
					state.BindName(catalog.DescriptorNameKey(&ns), ns.Address())
				case "destination_collision":
					n := *old
					n.ObjectName = "statistics_new"
					n.ObjectAddress.ObjectID += 10000
					n.ObjectRevision = old.Revision() + 1
					state.SetDescriptor(n.Address(), &n)
					state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				}
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
			}
			incumbent := e.CatalogRead()
			_, err = e.PublishCatalogOperation(mutation, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || e.CatalogRead().State() != incumbent.State() {
				t.Fatal("rejected mutation published", mode, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if stage.Base.Get(old.Address()) != old {
				t.Fatal("retained definition changed")
			}
		})
	}
}
