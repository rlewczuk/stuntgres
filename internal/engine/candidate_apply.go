package engine

import (
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// applyTo projects the candidate onto current, the published state that passed
// every publication fence, and returns the new unpublished state: default
// ACLs, removals, old names, descriptors, names and the operator and procedure
// name indexes. It runs with nothing held.
func (c *CatalogStage) applyTo(current *catalog.State) *catalog.State {
	next := catalog.CloneState(current)
	c.ApplyDefaultACLs(current, next)
	operators := catalog.OperatorIndexEdit{Index: current.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: current.ProcedureNames()}
	for a, removal := range c.Removals() {
		d := removal.Descriptor
		operators.Replace(current.Get(a), c.Get(a))
		procedures.Replace(current.Get(a), c.Get(a))
		next.RemoveDescriptor(a)
		next.RemoveDumpPriority(a)
		next.UnbindName(catalog.DescriptorNameKey(d))
	}
	for k := range c.OldNames() {
		next.UnbindName(k)
	}
	for a, d := range c.Descriptors() {
		if _, removed := c.Removal(a); !removed {
			operators.Replace(current.Get(a), d)
			procedures.Replace(current.Get(a), d)
		}
		next.SetDescriptor(a, d)
	}
	for k, a := range c.Names() {
		next.BindName(k, a)
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	return next
}

// changedAddresses lists the candidate's descriptor and removal addresses,
// sorted by class, object and sub ID. An address both removed and staged
// appears twice.
func (c *CatalogStage) changedAddresses() []ids.ObjectAddress {
	addresses := make([]ids.ObjectAddress, 0, c.DescriptorCount()+c.RemovalCount())
	for a := range c.Descriptors() {
		addresses = append(addresses, a)
	}
	for a := range c.Removals() {
		addresses = append(addresses, a)
	}
	sort.Slice(addresses, func(i, j int) bool {
		a, b := addresses[i], addresses[j]
		if a.ClassID != b.ClassID {
			return a.ClassID < b.ClassID
		}
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		return a.SubID < b.SubID
	})
	return addresses
}
