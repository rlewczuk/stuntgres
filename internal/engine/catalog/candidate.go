package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Candidate is the pure copy-on-write overlay of a catalog stage over
// an immutable base view: the descriptors it creates or replaces and their
// names, the name slots it touched, its removals and its default ACL changes.
// It never writes the base. CatalogStage embeds it and keeps the publication
// coordination.
type Candidate struct {
	Base Read
	// Descriptors and Names hold the created and replaced descriptors and
	// their name bindings.
	Descriptors map[ids.ObjectAddress]Descriptor
	Names       map[ids.NameKey]ids.ObjectAddress
	// Touched maps every name the candidate bound or vacated to the base
	// binding it observed, for publication validation.
	Touched map[ids.NameKey]ids.ObjectAddress
	// Removals are whole objects removed with their observed identity.
	Removals map[ids.ObjectAddress]Removal
	// OldNames are the base names that replacements vacated.
	OldNames map[ids.NameKey]bool
	// DefaultACLs are default ACL changes (a nil record removes its key);
	// DefaultReads are the base records the candidate observed.
	DefaultACLs  map[DefaultACLKey]*DefaultACLRecord
	DefaultReads map[DefaultACLKey]*DefaultACLRecord
}

// Removal is one staged removal and the descriptor it observed.
type Removal struct {
	Ref        ids.ObjectRef
	Descriptor Descriptor
}

// newCatalogCandidate returns an empty candidate over base.
func NewCandidate(base Read) Candidate {
	return Candidate{Base: base, Descriptors: make(map[ids.ObjectAddress]Descriptor), Names: make(map[ids.NameKey]ids.ObjectAddress), Touched: make(map[ids.NameKey]ids.ObjectAddress)}
}

// LookupName resolves the private candidate, including names vacated by a
// replacement. The original name remains in Touched for publication
// validation.
func (c *Candidate) LookupName(k ids.NameKey) ids.ObjectAddress {
	if address := c.Names[k]; address != (ids.ObjectAddress{}) {
		if _, removed := c.Removals[address]; !removed {
			return address
		}
	}
	address := c.Base.NameAddress(k)
	if _, removed := c.Removals[address]; removed {
		return ids.ObjectAddress{}
	}
	if replacement := c.Descriptors[address]; replacement != nil &&
		DescriptorNameKey(replacement) != k {
		return ids.ObjectAddress{}
	}
	return address
}

// Create stages d as a new object. Its address and name must be free in the
// base and in the candidate; removed generations free their name slots.
func (c *Candidate) Create(d Descriptor) error {
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	a := d.Address()
	if a.ObjectID == 0 || a.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid catalog object address")
	}
	if _, ok := c.Base.Find(a); ok {
		return Conflict{}
	}
	if _, ok := c.Descriptors[a]; ok {
		return Conflict{}
	}
	k := DescriptorNameKey(d)
	// One live-name check includes staged replacements and excludes removed
	// generations, whose private name slots can be reused by a new identity.
	if c.LookupName(k) != (ids.ObjectAddress{}) {
		return Conflict{}
	}
	c.Touched[k] = c.Base.NameAddress(k)
	c.Descriptors[a] = d
	c.Names[k] = a
	return nil
}

// Replace stages d as the next revision of an existing base object. A
// relation keeps its sticky index and subclass publication history.
func (c *Candidate) Replace(d Descriptor) error {
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	old, ok := c.Base.Descriptor(d.Address())
	if !ok {
		return Conflict{}
	}
	if previous, ok := old.(*Relation); ok && previous.History.HasIndexes {
		if next, ok := d.(*Relation); !ok || !next.History.HasIndexes {
			return fmt.Errorf("stuntgres: relation replacement clears index publication history")
		}
	}
	if previous, ok := old.(*Relation); ok && previous.History.HasSubclasses {
		if next, ok := d.(*Relation); !ok || !next.History.HasSubclasses {
			return fmt.Errorf("stuntgres: relation replacement clears subclass publication history")
		}
	}
	oldKey := DescriptorNameKey(old)
	newKey := DescriptorNameKey(d)
	if a := c.LookupName(newKey); a != (ids.ObjectAddress{}) && a != d.Address() {
		return Conflict{}
	}
	if previous := c.Descriptors[d.Address()]; previous != nil {
		delete(c.Names, DescriptorNameKey(previous))
	}
	if c.OldNames == nil {
		c.OldNames = make(map[ids.NameKey]bool)
	}
	c.OldNames[oldKey] = true
	c.Touched[oldKey] = c.Base.NameAddress(oldKey)
	c.Touched[newKey] = c.Base.NameAddress(newKey)
	c.Descriptors[d.Address()] = d
	c.Names[newKey] = d.Address()
	return nil
}

// RecordRemoval stages the removal of the whole object ref names, which
// currently holds d.
func (c *Candidate) RecordRemoval(ref ids.ObjectRef, d Descriptor) {
	if c.Removals == nil {
		c.Removals = make(map[ids.ObjectAddress]Removal)
	}
	c.Removals[ref.Address] = Removal{Ref: ref, Descriptor: d}
}

// State returns a new unpublished state: the base with the candidate's
// removals, vacated names, descriptors, names and default ACL changes
// applied. It has its own descriptor and name maps, and keeps the dump
// priorities of the objects that remain.
func (c *Candidate) State() *State {
	ds := make(map[ids.ObjectAddress]Descriptor, c.Base.DescriptorCount()+len(c.Descriptors))
	ns := make(map[ids.NameKey]ids.ObjectAddress, c.Base.NameCount()+len(c.Names))
	operators := OperatorIndexEdit{Index: c.Base.state.operatorNames}
	for k, v := range c.Base.Descriptors() {
		ds[k] = v
	}
	for k, v := range c.Base.Names() {
		ns[k] = v
	}
	for a, removal := range c.Removals {
		d := removal.Descriptor
		operators.Replace(c.Base.Get(a), c.Descriptors[a])
		delete(ds, a)
		delete(ns, DescriptorNameKey(d))
	}
	for k := range c.OldNames {
		delete(ns, k)
	}
	for k, v := range c.Descriptors {
		if _, removed := c.Removals[k]; !removed {
			operators.Replace(c.Base.Get(k), v)
		}
		ds[k] = v
	}
	for k, v := range c.Names {
		ns[k] = v
	}
	priorities := c.Base.state.dumpPriorities
	if len(priorities) != 0 && len(c.Removals) != 0 {
		priorities = make(map[ids.ObjectAddress]ids.OID, len(c.Base.state.dumpPriorities))
		for address, priority := range c.Base.state.dumpPriorities {
			if _, exists := ds[address]; exists {
				priorities[address] = priority
			}
		}
	}
	return &State{epoch: c.Base.Epoch(), namespaceGeneration: c.Base.state.namespaceGeneration, descriptors: ds, names: ns, operatorNames: operators.Index, defaultACLs: overlayDefaultACLs(c.Base.state.defaultACLs, c.DefaultACLs), dumpPriorities: priorities}
}
