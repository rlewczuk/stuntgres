package catalog

import (
	"fmt"
	"iter"
	"maps"

	"stuntgres/internal/engine/ids"
)

// Candidate is the pure copy-on-write overlay of a catalog stage over
// an immutable base view: the descriptors it creates or replaces and their
// names, the name slots it touched, its removals and its default ACL changes.
// It never writes the base. CatalogStage embeds it and keeps the publication
// coordination.
type Candidate struct {
	Base Read
	// descriptors and names hold the created and replaced descriptors and
	// their name bindings.
	descriptors map[ids.ObjectAddress]Descriptor
	names       map[ids.NameKey]ids.ObjectAddress
	// touched maps every name the candidate bound or vacated to the base
	// binding it observed, for publication validation.
	touched map[ids.NameKey]ids.ObjectAddress
	// removals are whole objects removed with their observed identity.
	removals map[ids.ObjectAddress]Removal
	// oldNames are the base names that replacements vacated.
	oldNames map[ids.NameKey]bool
	// defaultACLs are default ACL changes (a nil record removes its key);
	// defaultReads are the base records the candidate observed.
	defaultACLs  map[DefaultACLKey]*DefaultACLRecord
	defaultReads map[DefaultACLKey]*DefaultACLRecord
}

// Removal is one staged removal and the descriptor it observed.
type Removal struct {
	Ref        ids.ObjectRef
	Descriptor Descriptor
}

// NewCandidate returns an empty candidate over base.
func NewCandidate(base Read) Candidate {
	return Candidate{Base: base, descriptors: make(map[ids.ObjectAddress]Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress), touched: make(map[ids.NameKey]ids.ObjectAddress)}
}

// LookupName resolves the private candidate, including names vacated by a
// replacement. The original name remains in Touched for publication
// validation.
func (c *Candidate) LookupName(k ids.NameKey) ids.ObjectAddress {
	if address := c.names[k]; address != (ids.ObjectAddress{}) {
		if _, removed := c.removals[address]; !removed {
			return address
		}
	}
	address := c.Base.NameAddress(k)
	if _, removed := c.removals[address]; removed {
		return ids.ObjectAddress{}
	}
	if replacement := c.descriptors[address]; replacement != nil {
		if DescriptorNameKey(replacement) != k {
			return ids.ObjectAddress{}
		}
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
	if _, ok := c.descriptors[a]; ok {
		return Conflict{}
	}
	k := DescriptorNameKey(d)
	// One live-name check includes staged replacements and excludes removed
	// generations, whose private name slots can be reused by a new identity.
	if c.LookupName(k) != (ids.ObjectAddress{}) {
		return Conflict{}
	}
	c.touched[k] = c.Base.NameAddress(k)
	c.descriptors[a] = d
	c.names[k] = a
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
	if previous := c.descriptors[d.Address()]; previous != nil {
		delete(c.names, DescriptorNameKey(previous))
	}
	if c.oldNames == nil {
		c.oldNames = make(map[ids.NameKey]bool)
	}
	c.oldNames[oldKey] = true
	c.touched[oldKey] = c.Base.NameAddress(oldKey)
	c.touched[newKey] = c.Base.NameAddress(newKey)
	c.descriptors[d.Address()] = d
	c.names[newKey] = d.Address()
	return nil
}

// RecordRemoval retires the original base identity and any private rebound
// descriptor. The original ref and name observation remain publication fences.
func (c *Candidate) RecordRemoval(ref ids.ObjectRef, d Descriptor) {
	if c.removals == nil {
		c.removals = make(map[ids.ObjectAddress]Removal)
	}
	c.removals[ref.Address] = Removal{Ref: ref, Descriptor: d}
	if candidate := c.descriptors[ref.Address]; candidate != nil {
		key := DescriptorNameKey(candidate)
		if c.names[key] == ref.Address {
			delete(c.names, key)
		}
	}
	delete(c.descriptors, ref.Address)
	c.touched[DescriptorNameKey(d)] = ref.Address
}

// State returns a new unpublished state: the base with the candidate's
// removals, vacated names, descriptors, names and default ACL changes
// applied. Its descriptor and name maps share every unchanged part with the
// base, so it costs the size of the candidate, not of the catalog; writing to
// it never changes the base. It keeps the dump priorities of the objects that
// remain.
func (c *Candidate) State() *State {
	base := c.Base.state
	base.mustNotBulkLoad()
	ds := base.descriptors.Transient()
	ns := base.names.Transient()
	operators := OperatorIndexEdit{Index: base.operatorNames}
	procedures := ProcedureIndexEdit{Index: base.procedureNames}
	casts := &State{castPairs: base.castPairs}
	for a, removal := range c.removals {
		d := removal.Descriptor
		operators.Replace(c.Base.Get(a), c.descriptors[a])
		procedures.Replace(c.Base.Get(a), c.descriptors[a])
		casts.updateCastIndex(c.Base.Get(a), c.descriptors[a])
		ds.Delete(a)
		ns.Delete(DescriptorNameKey(d))
	}
	for k := range c.oldNames {
		ns.Delete(k)
	}
	for k, v := range c.descriptors {
		if _, removed := c.removals[k]; !removed {
			operators.Replace(c.Base.Get(k), v)
			procedures.Replace(c.Base.Get(k), v)
			casts.updateCastIndex(c.Base.Get(k), v)
		}
		ds.Set(k, v)
	}
	for k, v := range c.names {
		ns.Set(k, v)
	}
	next := &State{epoch: c.Base.Epoch(), namespaceGeneration: base.namespaceGeneration, descriptors: ds.Persistent(), names: ns.Persistent(), operatorNames: operators.Index, procedureNames: procedures.Index, castPairs: casts.castPairs, defaultACLs: overlayDefaultACLs(base.defaultACLs, c.defaultACLs), dumpPriorities: base.dumpPriorities}
	next.defaultClasses = base.defaultClasses
	for a := range c.removals {
		next.updateDefaultClasses(c.Base.Get(a), nil)
	}
	for a, d := range c.descriptors {
		next.updateDefaultClasses(c.Base.Get(a), d)
	}
	if len(base.dumpPriorities) != 0 && len(c.removals) != 0 {
		next.dumpPriorities = make(map[ids.ObjectAddress]ids.OID, len(base.dumpPriorities))
		for address, priority := range base.dumpPriorities {
			if _, exists := next.descriptors.Get(address); exists {
				next.dumpPriorities[address] = priority
			}
		}
	}
	return next
}

// Get and Find read only staged descriptors, not the base catalog.
func (c *Candidate) Get(a ids.ObjectAddress) Descriptor { return c.descriptors[a] }
func (c *Candidate) Find(a ids.ObjectAddress) (Descriptor, bool) {
	d, ok := c.descriptors[a]
	return d, ok
}

// Iterators share immutable payloads and visit overlay entries only. They are
// valid during the candidate's read phase, not concurrent with its mutation.
func (c *Candidate) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return maps.All(c.descriptors)
}
func (c *Candidate) DescriptorCount() int                             { return len(c.descriptors) }
func (c *Candidate) Names() iter.Seq2[ids.NameKey, ids.ObjectAddress] { return maps.All(c.names) }
func (c *Candidate) NameBinding(k ids.NameKey) (ids.ObjectAddress, bool) {
	a, ok := c.names[k]
	return a, ok
}
func (c *Candidate) Removal(a ids.ObjectAddress) (Removal, bool) {
	r, ok := c.removals[a]
	return r, ok
}
func (c *Candidate) Removals() iter.Seq2[ids.ObjectAddress, Removal] { return maps.All(c.removals) }
func (c *Candidate) RemovalCount() int                               { return len(c.removals) }

// NamespaceChanged reports whether the candidate creates, replaces or removes
// a namespace, so publication must advance the namespace generation.
func (c *Candidate) NamespaceChanged() bool {
	for a := range c.descriptors {
		if a.ClassID == ids.NamespaceClassID {
			return true
		}
	}
	for a := range c.removals {
		if a.ClassID == ids.NamespaceClassID {
			return true
		}
	}
	return false
}
func (c *Candidate) NameObservation(k ids.NameKey) (ids.ObjectAddress, bool) {
	a, ok := c.touched[k]
	return a, ok
}
func (c *Candidate) Touched() iter.Seq2[ids.NameKey, ids.ObjectAddress] { return maps.All(c.touched) }
func (c *Candidate) OldNames() iter.Seq2[ids.NameKey, bool]             { return maps.All(c.oldNames) }
func (c *Candidate) OldNameCount() int                                  { return len(c.oldNames) }
func (c *Candidate) DefaultACLChange(k DefaultACLKey) (*DefaultACLRecord, bool) {
	r, ok := c.defaultACLs[k]
	return r, ok
}
func (c *Candidate) DefaultACLs() iter.Seq2[DefaultACLKey, *DefaultACLRecord] {
	return maps.All(c.defaultACLs)
}
func (c *Candidate) DefaultACLCount() int { return len(c.defaultACLs) }
func (c *Candidate) DefaultObservation(k DefaultACLKey) (*DefaultACLRecord, bool) {
	r, ok := c.defaultReads[k]
	return r, ok
}
func (c *Candidate) DefaultReads() iter.Seq2[DefaultACLKey, *DefaultACLRecord] {
	return maps.All(c.defaultReads)
}

// ApplyDefaultACLs projects the delta onto the current publication source.
func (c *Candidate) ApplyDefaultACLs(source, next *State) {
	next.OverlayDefaultACLs(source, c.defaultACLs)
}
func (c *Candidate) DefaultRecordsUnchanged(current *State) bool {
	return DefaultRecordsUnchanged(current, c.defaultReads)
}

// UpdateDescriptor completes a caller-owned private copy without replaying
// Create/Replace, allocating revisions, or synchronizing rowtypes. CatalogStage
// still owns lifecycle checks and publication validation.
func (c *Candidate) UpdateDescriptor(d Descriptor) { c.descriptors[d.Address()] = d }

// RenameStagedDescriptor rebinds an existing private/reminted identity using its
// actual keys, including an array's explicit namespace override.
func (c *Candidate) RenameStagedDescriptor(previousKey ids.NameKey, next Descriptor, nextKey ids.NameKey) {
	delete(c.names, previousKey)
	c.descriptors[next.Address()] = next
	c.names[nextKey] = next.Address()
	c.touched[nextKey] = c.Base.NameAddress(nextKey)
}

// ForgetDescriptor removes only the private descriptor entry.
func (c *Candidate) ForgetDescriptor(a ids.ObjectAddress) { delete(c.descriptors, a) }

// ObserveDefault captures the original base input once, including nil absence.
func (c *Candidate) ObserveDefault(k DefaultACLKey) {
	if _, observed := c.defaultReads[k]; observed {
		return
	}
	if c.defaultReads == nil {
		c.defaultReads = make(map[DefaultACLKey]*DefaultACLRecord)
	}
	c.defaultReads[k] = c.Base.State().DefaultACL(k)
}

// SetDefaultACL stages a delta only. A present nil record is a tombstone;
// TEMP cleanup deliberately does not acquire an input observation.
func (c *Candidate) SetDefaultACL(k DefaultACLKey, record *DefaultACLRecord) {
	if c.defaultACLs == nil {
		c.defaultACLs = make(map[DefaultACLKey]*DefaultACLRecord)
	}
	c.defaultACLs[k] = record
}
