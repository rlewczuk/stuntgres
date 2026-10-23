package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// domainBasesCurrent reports whether every observed domain base is still the
// current descriptor. Terminal cleanup of a TEMP namespace also requires that
// the namespace holds nothing beyond the candidate's own removals. The root
// calls it under Engine.mu.
func (c *CatalogStage) domainBasesCurrent(current *catalog.State, bases []catalog.Descriptor, terminalCleanup bool) bool {
	for _, base := range bases {
		observed := base
		if c.Get(base.Address()) == base {
			observed = c.Base.Get(base.Address())
		}
		if current.Get(base.Address()) != observed {
			return false
		}
		if ns, ok := base.(*catalog.Namespace); ok && terminalCleanup && ns.Temporary != (ids.SessionToken{}) {
			for address, d := range current.Descriptors() {
				if _, removed := c.Removal(address); d.Namespace() == ns.Address() && (!removed || d != c.Base.Get(address)) {
					return false
				}
			}
		}
	}
	return true
}

// descriptorsCurrent reports whether current still matches every base input
// of the candidate: the namespaces its descriptors live in or move from, the
// contents of removed namespaces, its replaced and removed descriptors, its
// touched names and the identity of its removals in domain. The root calls it
// under Engine.mu.
func (c *CatalogStage) descriptorsCurrent(current *catalog.State, domain *ids.Domain) bool {
	for _, d := range c.Descriptors() {
		ns := d.Namespace()
		if ns != (ids.ObjectAddress{}) && c.Get(ns) == nil && current.Get(ns) != c.Base.Get(ns) {
			return false
		}
		// A namespace move still relies on source lookup authority. Fence
		// that original namespace as well as the destination, without
		// republishing it or observing unrelated namespace changes.
		if old := c.Base.Get(d.Address()); old != nil {
			source := old.Namespace()
			if source != ns && source != (ids.ObjectAddress{}) && c.Get(source) == nil && current.Get(source) != c.Base.Get(source) {
				return false
			}
		}
	}
	for a := range c.Removals() {
		if a.ClassID != ids.NamespaceClassID {
			continue
		}
		for address, d := range current.Descriptors() {
			if _, removed := c.Removal(address); d.Namespace() == a && (!removed || d != c.Base.Get(address)) {
				return false
			}
		}
	}
	for a := range c.Descriptors() {
		if current.Get(a) != c.Base.Get(a) {
			return false
		}
	}
	for a := range c.Removals() {
		if current.Get(a) != c.Base.Get(a) {
			return false
		}
	}
	for k, observed := range c.Touched() {
		if current.NameAddress(k) != observed {
			return false
		}
	}
	for a, removal := range c.Removals() {
		observed := removal.Descriptor
		d := current.Get(a)
		if removal.Ref.Domain() != domain || removal.Ref.Epoch != current.Epoch() || removal.Ref.Address != a || d == nil || d != observed || d.Revision() != observed.Revision() {
			return false
		}
	}
	return true
}

// accessMethodsCurrent adds the cross-object reads of access methods to the
// descriptor and name fences. They keep method retirement and new consumers
// atomic when another utility publishes between private validation and
// publication. The root calls it under Engine.mu; no name resolution,
// operation callback or allocation occurs.
func (c *CatalogStage) accessMethodsCurrent(current catalog.Read) bool {
	dependency := func(d catalog.Descriptor) ids.ObjectAddress {
		switch d := d.(type) {
		case *catalog.AccessMethod:
			return ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Handler}
		case *catalog.OperatorFamily:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *catalog.OperatorClass:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *catalog.Relation:
			if d.SQLIndex != nil {
				return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.SQLIndex.MethodIdentity()}
			}
		}
		return ids.ObjectAddress{}
	}
	for _, d := range c.Descriptors() {
		a := dependency(d)
		if a != (ids.ObjectAddress{}) && c.Get(a) == nil && current.Get(a) != c.Base.Get(a) {
			return false
		}
	}
	for a := range c.Removals() {
		if a.ClassID != ids.AccessMethodClassID && (a.ClassID != ids.ProcedureClassID || catalog.NativeHandler(a.ObjectID) == catalog.NoAccessMethod) {
			continue
		}
		for address, d := range current.Descriptors() {
			if d == c.Base.Get(address) {
				continue
			}
			if dependency(d) == a {
				return false
			}
		}
	}
	return true
}
