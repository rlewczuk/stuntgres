package session

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The journal bookkeeping below records private catalog changes against base,
// the engine's published catalog state, and builds the private read. The
// caller holds Engine.mu, so base is current and stable, and the session's
// statement admission, so the journal has one writer.

// PrivateRead returns the transaction's private catalog: base overlaid with
// c's default ACLs and descriptors and their name bindings, read through
// owner. The read is cached against base until c records another change. A
// nil journal reads base.
func (c *Journal) PrivateRead(owner *catalog.Owner, base *catalog.State) catalog.Read {
	if c == nil {
		return catalog.NewRead(owner, base)
	}
	if c.CachedBase == base && c.Cached.State() != nil {
		return c.Cached
	}
	if len(c.Descriptors) == 0 && len(c.DefaultACLs) == 0 {
		c.CachedBase = base
		c.Cached = catalog.NewRead(owner, base)
		return c.Cached
	}
	n := catalog.CloneState(base)
	n.OverlayDefaultACLs(base, c.DefaultACLs)
	operators := catalog.OperatorIndexEdit{Index: base.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: base.ProcedureNames()}
	for a, d := range c.Descriptors {
		operators.Replace(base.Get(a), d)
		procedures.Replace(base.Get(a), d)
		if old := n.Get(a); old != nil {
			n.UnbindName(catalog.DescriptorNameKey(old))
		}
		if d == nil {
			n.RemoveDescriptor(a)
			n.RemoveDumpPriority(a)
		} else {
			n.SetDescriptor(a, d)
		}
	}
	for _, d := range c.Descriptors {
		if d != nil {
			n.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	n.SetOperatorNames(operators.Index)
	n.SetProcedureNames(procedures.Index)
	c.CachedBase, c.Cached = base, catalog.NewRead(owner, n)
	return c.Cached
}

// RecordDescriptor records d (nil for a removal) as the private descriptor of
// a, first remembering base's descriptor of a and the base bindings of the
// old and new names. A removal forgets a's private index state.
func (c *Journal) RecordDescriptor(base *catalog.State, a ids.ObjectAddress, d catalog.Descriptor) {
	if _, ok := c.Originals[a]; !ok {
		c.Originals[a] = base.Get(a)
	}
	if original := c.Originals[a]; original != nil {
		k := catalog.DescriptorNameKey(original)
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = base.NameAddress(k)
		}
	}
	if d != nil {
		k := catalog.DescriptorNameKey(d)
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = base.NameAddress(k)
		}
	}
	c.Descriptors[a] = d
	if d == nil && c.Indexes != nil {
		c.Indexes.Forget(a)
	}
	c.CachedBase = nil
	c.Cached = catalog.Read{}
}

// RecordDefaultACL records record (nil for a removal) as the private default
// ACL of key, first remembering base's record.
func (c *Journal) RecordDefaultACL(base *catalog.State, key catalog.DefaultACLKey, record *catalog.DefaultACLRecord) {
	if c.DefaultOriginals == nil {
		c.DefaultOriginals = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		c.DefaultACLs = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
	}
	if _, exists := c.DefaultOriginals[key]; !exists {
		c.DefaultOriginals[key] = base.DefaultACL(key)
	}
	c.DefaultACLs[key] = record
	c.CachedBase = nil
	c.Cached = catalog.Read{}
}

// ObserveCreationACL returns the default ACL that an object of class created
// by owner in schema gets from view, after recording base's global and schema
// defaults as read unless the journal changed them.
func (c *Journal) ObserveCreationACL(base *catalog.State, view catalog.Read, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := catalog.DefaultACLKey{Creator: owner, Schema: namespace, Class: class}
		if _, private := c.DefaultACLs[key]; !private {
			if c.DefaultReads == nil {
				c.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
			}
			if _, exists := c.DefaultReads[key]; !exists {
				c.DefaultReads[key] = base.DefaultACL(key)
			}
		}
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return catalog.CreationDefaultACL(view, owner, schema, class)
}
