package table

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
)

// DropEffect is one ordered effect of a prepared drop. Exactly one kind is
// set; the consumer replays them into the statement's stage in order.
type DropEffect struct {
	Descriptor    catalog.Descriptor // replaced descriptor
	Remove        ids.ObjectRef      // removed object
	Before, After *catalog.Relation  // column projection of a relation
	Notice        string             // cascade notice
	Missing       *sqlstate.Error    // emitted notice
	RowType       ids.ObjectAddress  // removed row type
	IdentityRoots []ids.ObjectAddress
	IdentityState *catalog.State // nil: the preparation's base state
	Index         ids.ObjectAddress
	Layout        *index.LayoutLineage
	Internal      bool // PERFORM_DELETION_INTERNAL on this removal operation.
	Dropped       []catalog.DroppedObject
}

// Slots index only real ordered effects, never a second descriptor/name catalog.
// Indices are one-based so an address can have independent metadata and lineage.
type dropEffectSlot struct {
	descriptor, layout int
	removed            bool
}

// DropPreparation is the ordered effect log of one drop prepared before the
// statement's stage exists. The preparing statement owns it; it is used by
// one goroutine and holds no lock.
type DropPreparation struct {
	Old   *catalog.Relation
	Ref   ids.ObjectRef
	Prior *DropPreparation
	// Statement is the preparing statement's identity: compared with the
	// consuming statement, never used to reach it.
	Statement any
	TxRef     ids.TxRef
	Base      catalog.Read
	// Discovery is the authentic read returned by admission; it never
	// replaces the original Base proofs.
	Discovery catalog.Read
	Input     catalog.DefinitionReader
	Grants    []ids.ObjectRef
	DropFlags map[ids.ObjectAddress]catalog.DropProvenance

	next     *catalog.Relation
	effects  []DropEffect
	observed map[ids.ObjectAddress]catalog.Descriptor
	slots    map[ids.ObjectAddress]dropEffectSlot
}

// RecordEffect appends effect and indexes it by address.
func (prepared *DropPreparation) RecordEffect(effect DropEffect) {
	prepared.effects = append(prepared.effects, effect)
	prepared.indexEffect(len(prepared.effects), effect)
}

func (prepared *DropPreparation) indexEffect(index int, effect DropEffect) {
	var address ids.ObjectAddress
	switch {
	case effect.Descriptor != nil:
		address = effect.Descriptor.Address()
	case effect.Remove.Address != (ids.ObjectAddress{}):
		address = effect.Remove.Address
	case effect.After != nil:
		address = effect.After.Address()
	case effect.Layout != nil:
		address = effect.Index
	default:
		return
	}
	if prepared.slots == nil {
		prepared.slots = make(map[ids.ObjectAddress]dropEffectSlot)
	}
	slot := prepared.slots[address]
	if effect.Layout != nil {
		slot.layout = index
	} else {
		slot.descriptor = index
		slot.removed = slot.removed || effect.Remove.Address != (ids.ObjectAddress{})
	}
	prepared.slots[address] = slot
}

// Effects are the recorded effects in order.
func (prepared *DropPreparation) Effects() []DropEffect { return prepared.effects }

// NoticePosition is the position RewindNotices rewinds to.
func (prepared *DropPreparation) NoticePosition() int { return len(prepared.effects) }

// RewindNotices drops the cascade notices recorded after position.
func (prepared *DropPreparation) RewindNotices(position int) {
	// The position indexes effects, not notices. Keep preceding notices and
	// every non-notice effect in the actual provider order.
	kept := prepared.effects[:position]
	for _, effect := range prepared.effects[position:] {
		if effect.Notice == "" {
			kept = append(kept, effect)
		}
	}
	prepared.effects = kept
	clear(prepared.slots)
	for i, effect := range prepared.effects {
		prepared.indexEffect(i+1, effect)
	}
}

// Observe captures address's original descriptor once, before any read
// returns it: the Base descriptor, else the one in current.
func (prepared *DropPreparation) Observe(address ids.ObjectAddress, current catalog.Read) {
	if prepared.observed == nil {
		prepared.observed = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, captured := prepared.observed[address]; captured {
		return
	}
	descriptor, original := prepared.Base.Find(address)
	if !original {
		descriptor = current.Get(address)
	}
	prepared.observed[address] = descriptor
}

// CaptureObservation records descriptor as address's observation. An
// earlier, different observation is never overwritten: it reports false.
func (prepared *DropPreparation) CaptureObservation(address ids.ObjectAddress, descriptor catalog.Descriptor) bool {
	if prior, captured := prepared.observed[address]; captured && prior != descriptor {
		return false
	}
	if prepared.observed == nil {
		prepared.observed = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	prepared.observed[address] = descriptor
	return true
}

// ValidateObservations fences the preparation against r: the same statement,
// transaction, domain and epoch, and every observed descriptor unchanged. An
// additive catalog member is never treated as a replacement.
func (prepared *DropPreparation) ValidateObservations(statement any, txRef ids.TxRef, r catalog.Read) error {
	if prepared.Statement != statement || prepared.TxRef != txRef || prepared.Ref.Domain() != r.Owner().Domain || prepared.Ref.Epoch != r.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for address, descriptor := range prepared.observed {
		if r.Get(address) != descriptor {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

// Finish records the relation the prepared drop leaves.
func (prepared *DropPreparation) Finish(next *catalog.Relation) { prepared.next = next }

// Next is the relation the prepared drop leaves.
func (prepared *DropPreparation) Next() *catalog.Relation { return prepared.next }

// DefinitionDelta reports only the prepared drop's own descriptor effects.
func (prepared *DropPreparation) DefinitionDelta(address ids.ObjectAddress) (catalog.Descriptor, bool, bool) {
	if slot := prepared.slots[address]; slot.descriptor != 0 {
		if slot.removed {
			return nil, false, true
		}
		effect := prepared.effects[slot.descriptor-1]
		if effect.After != nil {
			return effect.After, true, true
		}
		return effect.Descriptor, effect.Descriptor != nil, true
	}
	return nil, false, false
}

// LayoutDelta reports only the prepared drop's own index layout effects.
func (prepared *DropPreparation) LayoutDelta(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	if slot := prepared.slots[address]; slot.layout != 0 {
		return *prepared.effects[slot.layout-1].Layout, true
	}
	return index.LayoutLineage{}, false
}

// Descriptor, Lookup and Descriptors are the read-only binder seam over this
// preparation's actual input and ordered effects. Consumers retain their normal
// resolution/dependency observations; these methods do not recertify or mutate
// them and do not construct a catalog generation.
func (prepared *DropPreparation) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	view := DropPreparationView{Base: prepared.Discovery, Preparation: prepared}
	if view.Removed(address) {
		return nil, false
	}
	if descriptor := view.Replacement(address); descriptor != nil {
		return descriptor, true
	}
	descriptor := view.InputDescriptor(address)
	return descriptor, descriptor != nil
}

func (prepared *DropPreparation) Lookup(key ids.NameKey) ids.ObjectAddress {
	address := prepared.Base.NameAddress(key)
	if prepared.Input != nil {
		address = prepared.Input.Lookup(key)
	}
	if address == (ids.ObjectAddress{}) && prepared.Base.NameAddress(key) == (ids.ObjectAddress{}) {
		address = prepared.Discovery.NameAddress(key)
	}
	if descriptor, present := prepared.Descriptor(address); present && catalog.DescriptorNameKey(descriptor) == key {
		return address
	}
	return ids.ObjectAddress{}
}

func (prepared *DropPreparation) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		view := DropPreparationView{Base: prepared.Discovery, Preparation: prepared}
		for address := range view.Descriptors() {
			if descriptor, present := prepared.Descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (prepared *DropPreparation) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return func(yield func(*catalog.Procedure) bool) {
		for d := range catalog.IndexedDefinitionProcedures(prepared.Discovery, prepared, namespace, name) {
			if !yield(d) {
				return
			}
		}
		if prepared.Input == nil {
			return
		}
		for candidate := range prepared.Input.ProceduresNamed(namespace, name) {
			if original, exists := prepared.Discovery.Procedure(candidate.Address()); exists && original.Namespace() == namespace && original.Name() == name {
				continue
			}
			descriptor, present := prepared.Descriptor(candidate.Address())
			if d, ok := descriptor.(*catalog.Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}

func (prepared *DropPreparation) HasDynamicProcedureName(name string) bool {
	return prepared.Discovery.State().HasDynamicProcedureName(name) || prepared.Input != nil && prepared.Input.HasDynamicProcedureName(name)
}

func (prepared *DropPreparation) HasProviderProcedureOverride(name string) bool {
	return prepared.Discovery.State().HasProviderProcedureOverride(name) || prepared.Input != nil && prepared.Input.HasProviderProcedureOverride(name)
}

// IndexLayout is the index layout the preparation leaves at address.
func (prepared *DropPreparation) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	return DropPreparationView{Base: prepared.Base, Preparation: prepared}.IndexLayout(address)
}
