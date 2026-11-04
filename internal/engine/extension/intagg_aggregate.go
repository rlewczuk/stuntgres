package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func IntaggDefinition(function, transition, final ids.OID) catalog.AggregateDefinition {
	return catalog.AggregateDefinition{Function: function, Kind: "n", Functions: [8]ids.OID{transition, final}, FinalModify: "r", MovingFinalModify: "r", TransitionType: 2281}
}

func validateOwnedAggregate(view catalog.Read, proc *catalog.Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg aggregate metadata") }
	if proc.RoutineKind != catalog.AggregateRoutine {
		if proc.Aggregate != nil {
			return invalid()
		}
		return nil
	}
	owned := proc.Aggregate
	if owned == nil || owned.Definition != IntaggDefinition(proc.Address().ObjectID, owned.Helpers[0].ID, owned.Helpers[1].ID) {
		return invalid()
	}
	origin := proc.Origin
	if origin == nil || !origin.Valid(proc.Address()) {
		return invalid()
	}
	canonical, ok := origin.Installation.Declarations[origin.Declaration].(*catalog.Procedure)
	if !ok || canonical.Aggregate == nil {
		return invalid()
	}
	for i, helper := range owned.Helpers {
		if helper.ID == 0 || helper.ID == owned.Helpers[1-i].ID || helper != canonical.Aggregate.Helpers[i] || origin.Installation.Members[i].ObjectID != helper.ID {
			return invalid()
		}
		current, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID})
		if !ok || current.Origin == nil || current.Origin.Installation != origin.Installation || current.Origin.Declaration != i || current.Implementation != StockIntagg {
			return invalid()
		}
		if _, err := ProcedureSymbol(view, current); err != nil {
			return err
		}
	}
	return nil
}

func remapOwnedAggregate(owned *catalog.OwnedAggregate, owner ids.ObjectAddress, mapping map[ids.ObjectAddress]ids.ObjectAddress) *catalog.OwnedAggregate {
	if owned == nil {
		return nil
	}
	n := *owned
	n.Definition.Function = owner.ObjectID
	for i, helper := range owned.Helpers {
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}]; ok {
			helper.ID = destination.ObjectID
		}
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helper.Namespace}]; ok {
			helper.Namespace = destination.ObjectID
		}
		n.Helpers[i] = helper
	}
	n.Definition.Functions[0], n.Definition.Functions[1] = n.Helpers[0].ID, n.Helpers[1].ID
	if catalog.SameAggregateDefinition(&n, owned) {
		return owned
	}
	return &n
}
