package table

import (
	"fmt"
	"iter"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

// DefinitionProviderRequest is one whole-relation provider a staged
// definition depends on, at the base epoch, and the lock it is held with.
type DefinitionProviderRequest struct {
	Ref  ids.ObjectRef
	Mode lock.Mode
}

// DefinitionProviders collects the incumbent relations the staged
// descriptors depend on: LIKE sources, view leaves and dependencies, CHECKs,
// defaults, generations and index keys and predicates. Each original
// attribute reference is proved before it becomes a whole-object request.
// The requests are sorted by OID; an inheritance parent of a staged relation
// is requested with ShareUpdateExclusive, every other provider with
// AccessShare. retained maps each provider first referenced at an older
// epoch to its base descriptor, which must still be current after the wait.
// Providers the stage created itself need no lock. A nil stage has no LIKE
// sources.
func (s *Stage) DefinitionProviders(base catalog.Read, staged ddl.Stage, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], domain *ids.Domain) ([]DefinitionProviderRequest, map[ids.ObjectAddress]catalog.Descriptor, error) {
	var refs []ids.ObjectRef
	var retained map[ids.ObjectAddress]catalog.Descriptor
	add := func(ref ids.ObjectRef, d *catalog.Relation) error {
		address := ref.Address
		address.SubID = 0
		if address.ClassID != ids.RelationClassID || d != nil && address == d.Address() {
			return nil
		}
		if _, incumbent := base.Find(address); !incumbent {
			// Newly created providers belong to the private atomic candidate.
			provider, ok := staged.Get(address).(*catalog.Relation)
			if !ok || provider.Address() != address || ref.Domain() != domain || ref.Epoch != base.Epoch() {
				return fmt.Errorf("stuntgres: invalid staged definition provider")
			}
			return nil
		}
		valid := stored.ReferenceEpochValid(base, d, ref)
		if d != nil && d.View != nil {
			// The stage's evolved consumer retains the incumbent definition's
			// immutable provenance; old epochs never become generic stage refs.
			incumbent, _ := base.Relation(d.Address())
			valid = incumbent != nil && incumbent.View == d.View && stored.ViewReferenceValid(base, incumbent, ref)
			if ref.Epoch == base.Epoch() {
				valid = stored.ViewReferenceValid(base, d, ref)
			}
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid retained definition provider")
		}
		// Prove the original attribute reference before using a whole-object lock.
		ref.Address = address
		if ref.Epoch != base.Epoch() {
			if retained == nil {
				retained = make(map[ids.ObjectAddress]catalog.Descriptor)
			}
			retained[ref.Address] = base.Get(ref.Address)
		}
		ref.Epoch = base.Epoch()
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		return nil
	}
	if s != nil {
		for _, descriptor := range s.LikeSources {
			if err := add(domain.Ref(base.Epoch(), descriptor.Address()), nil); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if err := addRelationProviders(d, add); err != nil {
			return nil, nil, err
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	requests := make([]DefinitionProviderRequest, len(refs))
	for i, ref := range refs {
		mode := lock.AccessShare
		for _, descriptor := range descriptors {
			if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance.HasParent(ref.Address) {
				mode = lock.ShareUpdateExclusive
				break
			}
		}
		requests[i] = DefinitionProviderRequest{Ref: ref, Mode: mode}
	}
	return requests, retained, nil
}

// addRelationProviders passes every provider reference of d's stored
// definitions to add, with d as the consumer.
func addRelationProviders(d *catalog.Relation, add func(ids.ObjectRef, *catalog.Relation) error) error {
	if d.View != nil {
		for _, leaf := range stored.ViewOf(d).Sources() {
			if err := add(leaf.Ref, d); err != nil {
				return err
			}
		}
		for _, dependency := range stored.ViewOf(d).Dependencies() {
			if err := add(dependency.Ref, d); err != nil {
				return err
			}
		}
	}
	for _, c := range d.Constraints.Check {
		for _, dep := range c.Dependencies {
			if err := add(dep.Ref, d); err != nil {
				return err
			}
		}
	}
	for _, definition := range d.ColumnMeta.Defaults {
		for _, ref := range definition.Dependencies {
			if err := add(ref, d); err != nil {
				return err
			}
		}
	}
	for _, definition := range d.ColumnMeta.Generations {
		for _, ref := range definition.Dependencies {
			if err := add(ref, d); err != nil {
				return err
			}
		}
	}
	if d.SQLIndex != nil {
		for _, key := range d.SQLIndex.Keys {
			if key.Derived == nil {
				continue
			}
			for _, ref := range key.Derived.Dependencies {
				if err := add(ref, d); err != nil {
					return err
				}
			}
		}
		if predicate := d.SQLIndex.Predicate; predicate != nil {
			for _, ref := range predicate.Dependencies {
				if err := add(ref, d); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
