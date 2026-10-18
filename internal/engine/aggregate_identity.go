package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// The actual installed aggregate owns these lookup-only aliases. They are not
// descriptors, extension members, executable functions, or pg_proc SQL rows.
type ownedAggregate struct {
	definition aggregateDefinition
	helpers    [2]routineIdentity
}

func intaggDefinition(function, transition, final ids.OID) aggregateDefinition {
	return aggregateDefinition{function: function, kind: "n", functions: [8]ids.OID{transition, final}, finalModify: "r", movingFinalModify: "r", transitionType: 2281}
}

// A helper's dependencies are dependencies on this real aggregate descriptor,
// never invented ObjectRefs for metadata-only identities.
func routineDescriptorOwner(view CatalogRead, id ids.OID) *procedureDescriptor {
	if p, ok := view.state.descriptors[ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: id}].(*procedureDescriptor); ok {
		return p
	}
	for _, d := range view.state.descriptors {
		if p, ok := d.(*procedureDescriptor); ok && p.aggregate != nil {
			for _, helper := range p.aggregate.helpers {
				if helper.id == id {
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
		if proc.aggregate != nil {
			return invalid()
		}
		return nil
	}
	owned := proc.aggregate
	if owned == nil || owned.definition != intaggDefinition(proc.address.ObjectID, owned.helpers[0].id, owned.helpers[1].id) {
		return invalid()
	}
	for i, helper := range owned.helpers {
		name, signature := "int_agg_state", procedureSignature("2281,23")
		if i == 1 {
			name, signature = "int_agg_final_array", "2281"
		}
		if helper.id == 0 || helper.id == owned.helpers[1-i].id || helper.namespace != proc.namespace.ObjectID || helper.name != name || helper.signature != signature {
			return invalid()
		}
		if _, builtin := routineByOID[helper.id]; builtin {
			return invalid()
		}
		for address, d := range view.state.descriptors {
			if address.ObjectID == helper.id {
				return invalid()
			}
			if other, ok := d.(*procedureDescriptor); ok && other != proc && other.aggregate != nil {
				for _, alias := range other.aggregate.helpers {
					if alias.id == helper.id || alias.namespace == helper.namespace && alias.name == helper.name && alias.signature == helper.signature {
						return invalid()
					}
				}
			}
		}
		if view.state.names[ids.NameKey{ClassID: ProcedureClassID, Namespace: proc.namespace, Name: helper.name, Signature: string(helper.signature)}] != (ids.ObjectAddress{}) {
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
	n.definition.function = owner.ObjectID
	for i, helper := range owned.helpers {
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: helper.id}]; ok {
			helper.id = destination.ObjectID
		}
		helper.namespace = namespace.ObjectID
		n.helpers[i] = helper
	}
	n.definition.functions[0], n.definition.functions[1] = n.helpers[0].id, n.helpers[1].id
	if n == *owned {
		return owned
	}
	return &n
}
