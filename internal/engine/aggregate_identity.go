package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// The actual installed aggregate owns these lookup-only aliases. They are not
// descriptors, extension members, executable functions, or pg_proc SQL rows.
type ownedAggregate struct {
	Definition aggregateDefinition
	Helpers    [2]routineIdentity
}

func intaggDefinition(function, transition, final ids.OID) aggregateDefinition {
	return aggregateDefinition{Function: function, Kind: "n", Functions: [8]ids.OID{transition, final}, FinalModify: "r", MovingFinalModify: "r", TransitionType: 2281}
}

// A helper's dependencies are dependencies on this real aggregate descriptor,
// never invented ObjectRefs for metadata-only identities.
func routineDescriptorOwner(view CatalogRead, id ids.OID) *procedureDescriptor {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return p
	}
	for _, d := range view.Descriptors() {
		if p, ok := d.(*procedureDescriptor); ok && p.Aggregate != nil {
			for _, helper := range p.Aggregate.Helpers {
				if helper.ID == id {
					return p
				}
			}
		}
	}
	return nil
}

func validateOwnedAggregate(view CatalogRead, proc *procedureDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg aggregate metadata") }
	if proc.name != "int_array_aggregate" {
		if proc.Aggregate != nil {
			return invalid()
		}
		return nil
	}
	owned := proc.Aggregate
	if owned == nil || owned.Definition != intaggDefinition(proc.address.ObjectID, owned.Helpers[0].ID, owned.Helpers[1].ID) {
		return invalid()
	}
	for i, helper := range owned.Helpers {
		name, signature := "int_agg_state", procedureSignature("2281,23")
		if i == 1 {
			name, signature = "int_agg_final_array", "2281"
		}
		if helper.ID == 0 || helper.ID == owned.Helpers[1-i].ID || helper.Namespace != proc.namespace.ObjectID || helper.Name != name || helper.Signature != signature {
			return invalid()
		}
		if _, builtin := routineByOID[helper.ID]; builtin {
			return invalid()
		}
		for address, d := range view.Descriptors() {
			if address.ObjectID == helper.ID {
				return invalid()
			}
			if other, ok := d.(*procedureDescriptor); ok && other != proc && other.Aggregate != nil {
				for _, alias := range other.Aggregate.Helpers {
					if alias.ID == helper.ID || alias.Namespace == helper.Namespace && alias.Name == helper.Name && alias.Signature == helper.Signature {
						return invalid()
					}
				}
			}
		}
		if view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: proc.namespace, Name: helper.Name, Signature: string(helper.Signature)}) != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func remapOwnedAggregate(owned *ownedAggregate, owner, namespace ids.ObjectAddress, mapping map[ids.ObjectAddress]ids.ObjectAddress) *ownedAggregate {
	if owned == nil {
		return nil
	}
	n := *owned
	n.Definition.Function = owner.ObjectID
	for i, helper := range owned.Helpers {
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}]; ok {
			helper.ID = destination.ObjectID
		}
		helper.Namespace = namespace.ObjectID
		n.Helpers[i] = helper
	}
	n.Definition.Functions[0], n.Definition.Functions[1] = n.Helpers[0].ID, n.Helpers[1].ID
	if n == *owned {
		return owned
	}
	return &n
}
