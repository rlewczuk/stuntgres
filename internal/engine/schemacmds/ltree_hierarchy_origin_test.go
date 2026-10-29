package schemacmds_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestLtreeHierarchySurvivorAuthority(t *testing.T) {
	e, op, origin, _ := lqueryProviderInstalled(t, false)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "uuid-ossp", "public"))
	view := e.CatalogRead()
	recipe := origin.Installation
	enginetest.RoleExecute(t, e, op, "DROP EXTENSION ltree")
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "ltree", "public"))
	other := e.CatalogRead().MustExtension(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"})).Installation
	for _, foreign := range []bool{false, true} {
		state := catalog.CloneState(view.State())
		receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
		received := *receiver
		received.Members = slices.Clone(receiver.Members)
		for _, address := range origin.Members {
			d := state.Get(address)
			membership := ids.ObjectAddress{}
			if foreign {
				membership = receiver.Address()
				received.Members = append(received.Members, address)
			}
			next, err := catalog.WithExtensionMembership(d, membership, d.Revision())
			if err != nil {
				t.Fatal(err)
			}
			state.SetDescriptor(address, next)
		}
		if foreign {
			state.SetDescriptor(received.Address(), &received)
		}
		state.UnbindName(catalog.DescriptorNameKey(origin))
		state.RemoveDescriptor(origin.Address())
		current := view.WithState(state)
		for index := range int(sqltype.LtreeHierarchyOperationCount) {
			procedure := current.MustProcedure(recipe.Members[extension.LtreeHierarchyProcedureStart+index])
			operation, ok := extension.LtreeHierarchyProcedure(current, procedure)
			if !ok || operation != sqltype.LtreeHierarchyOperation(index) {
				t.Fatal("surviving hierarchy role lost", foreign, index)
			}
			if symbol, err := extension.ProcedureSymbol(current, procedure); err != nil || symbol != procedure.Display.Source {
				t.Fatal("surviving symbol lost", symbol, err)
			}
			operator, _ := current.Operator(recipe.Members[extension.LtreeHierarchyOperatorStart+index])
			if err := catalog.ValidateInstalledClassOrigin(current, operator); err != nil {
				t.Fatal("surviving operator lost", err)
			}
			for _, cause := range []string{"copied_origin", "foreign_cohort", "missing_origin", "principal", "source", "signature", "result", "provider"} {
				broken := catalog.CloneState(state)
				bad := *procedure
				switch cause {
				case "copied_origin":
					copied := *recipe
					bad.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: procedure.Origin.Declaration}
				case "foreign_cohort":
					bad.Origin = &catalog.InstalledOrigin{Installation: other, Declaration: procedure.Origin.Declaration}
				case "missing_origin":
					bad.Origin = nil
				case "principal":
					bad.Owner = (&authorization.Domain{}).Principal(bad.Owner.ID())
				case "source":
					bad.Display.Source = "ltree_in"
				case "signature":
					bad.Signature = "25,25"
				case "result":
					bad.Result = 25
				case "provider":
					bad.Implementation = extension.StockHstore
				}
				broken.SetDescriptor(bad.Address(), &bad)
				if _, ok := extension.LtreeHierarchyProcedure(view.WithState(broken), &bad); ok {
					t.Fatal("forged hierarchy authority", cause, index)
				}
				if err := catalog.ValidateInstalledClassOrigin(view.WithState(broken), operator); err == nil {
					t.Fatal("operator ignored current backing authority", cause, index)
				}
			}
		}
	}
}
