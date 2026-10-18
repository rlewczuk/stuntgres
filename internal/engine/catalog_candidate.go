package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// catalogCandidate is the pure copy-on-write overlay of a catalog stage over
// an immutable base view: the descriptors it creates or replaces and their
// names, the name slots it touched, its removals and its default ACL changes.
// It never writes the base. CatalogStage embeds it and keeps the publication
// coordination.
type catalogCandidate struct {
	Base CatalogRead
	// Descriptors and Names hold the created and replaced descriptors and
	// their name bindings.
	Descriptors map[ids.ObjectAddress]Descriptor
	Names       map[ids.NameKey]ids.ObjectAddress
	// Touched maps every name the candidate bound or vacated to the base
	// binding it observed, for publication validation.
	Touched map[ids.NameKey]ids.ObjectAddress
	// Removals are whole objects removed with their observed identity.
	Removals map[ids.ObjectAddress]catalogRemoval
	// OldNames are the base names that replacements vacated.
	OldNames map[ids.NameKey]bool
	// DefaultACLs are default ACL changes (a nil record removes its key);
	// DefaultReads are the base records the candidate observed.
	DefaultACLs  map[defaultACLKey]*defaultACLRecord
	DefaultReads map[defaultACLKey]*defaultACLRecord
}

// catalogRemoval is one staged removal and the descriptor it observed.
type catalogRemoval struct {
	Ref        ids.ObjectRef
	Descriptor Descriptor
}

// newCatalogCandidate returns an empty candidate over base.
func newCatalogCandidate(base CatalogRead) catalogCandidate {
	return catalogCandidate{Base: base, Descriptors: make(map[ids.ObjectAddress]Descriptor), Names: make(map[ids.NameKey]ids.ObjectAddress), Touched: make(map[ids.NameKey]ids.ObjectAddress)}
}

// LookupName resolves the private candidate, including names vacated by a
// replacement. The original name remains in Touched for publication
// validation.
func (c *catalogCandidate) LookupName(k ids.NameKey) ids.ObjectAddress {
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
		descriptorNameKey(replacement) != k {
		return ids.ObjectAddress{}
	}
	return address
}

// Create stages d as a new object. Its address and name must be free in the
// base and in the candidate; removed generations free their name slots.
func (c *catalogCandidate) Create(d Descriptor) error {
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	a := d.Address()
	if a.ObjectID == 0 || a.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid catalog object address")
	}
	if _, ok := c.Base.Find(a); ok {
		return CatalogConflict{}
	}
	if _, ok := c.Descriptors[a]; ok {
		return CatalogConflict{}
	}
	k := descriptorNameKey(d)
	// One live-name check includes staged replacements and excludes removed
	// generations, whose private name slots can be reused by a new identity.
	if c.LookupName(k) != (ids.ObjectAddress{}) {
		return CatalogConflict{}
	}
	c.Touched[k] = c.Base.NameAddress(k)
	c.Descriptors[a] = d
	c.Names[k] = a
	return nil
}

// Replace stages d as the next revision of an existing base object. A
// relation keeps its sticky index and subclass publication history.
func (c *catalogCandidate) Replace(d Descriptor) error {
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	old, ok := c.Base.Descriptor(d.Address())
	if !ok {
		return CatalogConflict{}
	}
	if previous, ok := old.(*relationDescriptor); ok && previous.HasIndexes {
		if next, ok := d.(*relationDescriptor); !ok || !next.HasIndexes {
			return fmt.Errorf("stuntgres: relation replacement clears index publication history")
		}
	}
	if previous, ok := old.(*relationDescriptor); ok && previous.HasSubclasses {
		if next, ok := d.(*relationDescriptor); !ok || !next.HasSubclasses {
			return fmt.Errorf("stuntgres: relation replacement clears subclass publication history")
		}
	}
	oldKey := descriptorNameKey(old)
	newKey := descriptorNameKey(d)
	if a := c.LookupName(newKey); a != (ids.ObjectAddress{}) && a != d.Address() {
		return CatalogConflict{}
	}
	if previous := c.Descriptors[d.Address()]; previous != nil {
		delete(c.Names, descriptorNameKey(previous))
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
func (c *catalogCandidate) RecordRemoval(ref ids.ObjectRef, d Descriptor) {
	if c.Removals == nil {
		c.Removals = make(map[ids.ObjectAddress]catalogRemoval)
	}
	c.Removals[ref.Address] = catalogRemoval{Ref: ref, Descriptor: d}
}

// State returns a new unpublished state: the base with the candidate's
// removals, vacated names, descriptors, names and default ACL changes
// applied. It has its own descriptor and name maps, and keeps the dump
// priorities of the objects that remain.
func (c *catalogCandidate) State() *catalogState {
	ds := make(map[ids.ObjectAddress]Descriptor, c.Base.DescriptorCount()+len(c.Descriptors))
	ns := make(map[ids.NameKey]ids.ObjectAddress, c.Base.NameCount()+len(c.Names))
	operators := operatorIndexEdit{Index: c.Base.state.operatorNames}
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
		delete(ns, descriptorNameKey(d))
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
	return &catalogState{epoch: c.Base.Epoch(), namespaceGeneration: c.Base.state.namespaceGeneration, descriptors: ds, names: ns, operatorNames: operators.Index, defaultACLs: overlayDefaultACLs(c.Base.state.defaultACLs, c.DefaultACLs), dumpPriorities: priorities}
}
