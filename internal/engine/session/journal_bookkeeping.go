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
// owner. The immutable State is cached against base until a private-read input
// changes; each read is rebound to its caller's owner. A nil journal reads base.
func (c *Journal) PrivateRead(owner *catalog.Owner, base *catalog.State) catalog.Read {
	if c == nil {
		return catalog.NewRead(owner, base)
	}
	c.lockPrivateRead()
	defer c.unlockPrivateRead()
	if c.CachedBase == base && c.Cached.State() != nil {
		c.Cached = catalog.NewRead(owner, c.Cached.State())
		return c.Cached
	}
	if !c.ParameterWritten && len(c.Descriptors) == 0 && len(c.DefaultACLs) == 0 && len(c.InitialPrivileges) == 0 && len(c.SecurityLabels) == 0 {
		c.CachedBase = base
		c.Cached = catalog.NewRead(owner, base)
		return c.Cached
	}
	n := catalog.CloneState(base)
	if c.ParameterWritten {
		n.SetParameterIncarnation(c.ParameterIncarnation)
	}
	n.OverlayDefaultACLs(base, c.DefaultACLs)
	n.OverlayInitialPrivileges(base, c.InitialPrivileges)
	n.OverlaySecurityLabels(base, c.SecurityLabels)
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

// InvalidatePrivateRead forgets the cached projection after a descriptor,
// default ACL, initial privilege, security label or parameter input changes.
// It never modifies the immutable State retained by earlier clones/readers.
func (c *Journal) InvalidatePrivateRead() {
	c.lockPrivateRead()
	defer c.unlockPrivateRead()
	c.CachedBase = nil
	c.Cached = catalog.Read{}
}

// RecordDescriptor records d (nil for a removal) as the private descriptor of
// a, first remembering base's descriptor of a and the base bindings of the
// old and new names. A removal forgets a's private index state.
func (c *Journal) RecordDescriptor(base *catalog.State, a ids.ObjectAddress, d catalog.Descriptor) {
	if a.ClassID == ids.ParameterClassID && !c.ParameterWritten {
		c.ParameterWritten = true
		c.ParameterIncarnation = base.ParameterIncarnation()
	}
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
	_, present := c.Descriptors[a]
	c.Descriptors[a] = d
	if d == nil && c.Indexes != nil {
		c.Indexes.Forget(a)
	}
	serial := uint64(1)
	if c.descriptorEdits != nil {
		serial = c.descriptorEdits.serial + 1
	}
	c.descriptorEdits = &descriptorEdit{address: a, serial: serial, prev: c.descriptorEdits}
	if !present {
		c.descriptorEditKeys++
	}
	c.InvalidatePrivateRead()
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
	c.InvalidatePrivateRead()
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

// RecordCandidateDefaults records the default ACLs candidate read, unless c
// changed them, and then candidate's default ACL changes against base.
func (c *Journal) RecordCandidateDefaults(base *catalog.State, candidate *catalog.Candidate) {
	c.RecordCandidateInitialPrivileges(base, candidate)
	c.RecordCandidateSecurityLabels(base, candidate)
	for key, record := range candidate.DefaultReads() {
		if _, private := c.DefaultACLs[key]; private {
			continue
		}
		if c.DefaultReads == nil {
			c.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if _, exists := c.DefaultReads[key]; !exists {
			c.DefaultReads[key] = record
		}
	}
	for key, record := range candidate.DefaultACLs() {
		c.RecordDefaultACL(base, key, record)
	}
}

// MergeCandidate records candidate's changes and removals against base after
// checking that base still holds, for each of them, the descriptor observed
// holds, or c's original where c already changed it. On a conflict it
// records nothing.
func (c *Journal) MergeCandidate(base, observed *catalog.State, candidate *catalog.Candidate) error {
	if err := c.mergeSecurityLabels(base, observed, candidate); err != nil {
		return err
	}
	for address, record := range candidate.InitialReads() {
		expected := record
		if original, private := c.InitialOriginals[address]; private {
			expected = original
		}
		if base.InitialPrivilege(address) != expected {
			return catalog.Conflict{}
		}
		object := address
		object.SubID = 0
		if !c.mergeObserved(base, observed, object) {
			return catalog.Conflict{}
		}
	}
	for a := range candidate.Descriptors() {
		if !c.mergeObserved(base, observed, a) {
			return catalog.Conflict{}
		}
	}
	for a := range candidate.Removals() {
		if !c.mergeObserved(base, observed, a) {
			return catalog.Conflict{}
		}
	}
	for address := range candidate.InitialReads() {
		address.SubID = 0
		if _, exists := c.Originals[address]; !exists {
			c.Originals[address] = base.Get(address)
		}
	}
	for a, d := range candidate.Descriptors() {
		c.RecordDescriptor(base, a, d)
	}
	for a := range candidate.Removals() {
		c.RecordDescriptor(base, a, nil)
	}
	c.RecordCandidateInitialPrivileges(base, candidate)
	c.observeSecurityLabelObjects(base, candidate)
	return nil
}

func (c *Journal) mergeObserved(base, observed *catalog.State, a ids.ObjectAddress) bool {
	expected := observed.Get(a)
	if original, private := c.Originals[a]; private {
		expected = original
	}
	return base.Get(a) == expected
}
