package catalog

import (
	"stuntgres/internal/engine/ids"
)

// DomainBasesCurrent reports whether every observed domain base is still the
// current descriptor. Terminal cleanup of a TEMP namespace also requires that
// the namespace holds nothing beyond the candidate's own removals. The root
// calls it under Engine.mu.
func (c *Candidate) DomainBasesCurrent(current *State, bases []Descriptor, terminalCleanup bool) bool {
	for _, base := range bases {
		observed := base
		if c.Get(base.Address()) == base {
			observed = c.Base.Get(base.Address())
		}
		if current.Get(base.Address()) != observed {
			return false
		}
		if ns, ok := base.(*Namespace); ok && terminalCleanup && ns.Temporary != (ids.SessionToken{}) {
			for address, d := range current.Descriptors() {
				if _, removed := c.Removal(address); d.Namespace() == ns.Address() && (!removed || d != c.Base.Get(address)) {
					return false
				}
			}
		}
	}
	return true
}

// DescriptorsCurrent reports whether current still matches every base input
// of the candidate: the namespaces its descriptors live in or move from, the
// contents of removed namespaces, its replaced and removed descriptors, its
// touched names and the identity of its removals in domain. The root calls it
// under Engine.mu.
func (c *Candidate) DescriptorsCurrent(current *State, domain *ids.Domain) bool {
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

// AccessMethodsCurrent adds the cross-object reads of access methods to the
// descriptor and name fences. They keep method retirement and new consumers
// atomic when another utility publishes between private validation and
// publication. The root calls it under Engine.mu; no name resolution,
// operation callback or allocation occurs.
func (c *Candidate) AccessMethodsCurrent(current Read) bool {
	dependency := func(d Descriptor) ids.ObjectAddress {
		switch d := d.(type) {
		case *AccessMethod:
			return ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Handler}
		case *OperatorFamily:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *OperatorClass:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *Relation:
			if d.SQLIndex != nil {
				return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.SQLIndex.MethodIdentity()}
			}
			if d.TableMethod != 0 {
				return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.TableMethod}
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
		procedure, handler := c.Base.Procedure(a)
		if a.ClassID != ids.AccessMethodClassID && (a.ClassID != ids.ProcedureClassID || !handler || procedure.AccessMethod == NoAccessMethod) {
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

// DefaultPreflight reports a Conflict unless every default ACL record the
// candidate read is unchanged in current and every current record of a
// schema it removes was observed. The root calls it under Engine.mu.
func (c *Candidate) DefaultPreflight(current *State) error {
	return c.defaultPreflight(current, nil)
}

// DefaultPreflightWithTemporaryNamespaces accepts only exact TEMP namespace
// creation intents authenticated by the source journal capture, including the
// exact own-pending label pointer and original committed label absence.
func (c *Candidate) DefaultPreflightWithTemporaryNamespaces(current *State, namespaces []*Namespace) error {
	for _, ns := range namespaces {
		if ns == nil || ns.Address().ClassID != ids.NamespaceClassID || ns.Address().ObjectID == 0 || ns.Address().SubID != 0 ||
			ns.Temporary.ID() == 0 || ns.Temporary.Domain() == nil || ns.Temporary.Domain() != c.Base.Domain() ||
			ns.Temporary.IsDetached() || ns.Temporary.Private() != nil || c.Base.Get(ns.Address()) != ns || current.Get(ns.Address()) != nil {
			return Conflict{}
		}
	}
	return c.defaultPreflight(current, namespaces)
}

func (c *Candidate) defaultPreflight(current *State, namespaces []*Namespace) error {
	if err := c.ParameterPreflight(current); err != nil {
		return err
	}
	if !c.DefaultRecordsUnchanged(current) || !c.InitialPrivilegesUnchanged(current) || !c.securityLabelsUnchanged(current, namespaces) {
		return Conflict{}
	}
	for key := range current.DefaultACLs() {
		if _, removed := c.Removal(key.Schema); !removed {
			continue
		}
		if _, observed := c.DefaultObservation(key); !observed {
			return Conflict{}
		}
	}
	return nil
}
