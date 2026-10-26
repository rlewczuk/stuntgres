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
	for i, helper := range owned.Helpers {
		name, signature := "int_agg_state", catalog.ProcedureSignature("2281,23")
		if i == 1 {
			name, signature = "int_agg_final_array", "2281"
		}
		ext, ok := view.Extension(proc.Extension)
		if !ok || helper.ID == 0 || helper.ID == owned.Helpers[1-i].ID || helper.Namespace != ext.TargetNamespace.ObjectID || helper.Name != name || helper.Signature != signature {
			return invalid()
		}
		if _, builtin := catalog.BuiltinRoutineIdentityByOID(helper.ID); builtin {
			return invalid()
		}
		for address, d := range view.Descriptors() {
			if address.ObjectID == helper.ID {
				return invalid()
			}
			if other, ok := d.(*catalog.Procedure); ok && other != proc && other.Aggregate != nil {
				for _, alias := range other.Aggregate.Helpers {
					if alias.ID == helper.ID || alias.Namespace == helper.Namespace && alias.Name == helper.Name && alias.Signature == helper.Signature {
						return invalid()
					}
				}
			}
		}
		if view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helper.Namespace}, Name: helper.Name, Signature: string(helper.Signature)}) != (ids.ObjectAddress{}) {
			return invalid()
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
