package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

// Persistent definition holds use the incumbent ticket and replacement fence.
// Admission occurs outside Engine.mu; after a wait identities are opened anew,
// never retargeted by name or evaluated to establish a dependency.
func (s *StatementScope) acquireDefinitionProviders(stage *CatalogStage) error {
	if err := s.acquireForeignKeyDefinitionProviders(stage); err != nil {
		return err
	}
	var refs []ids.ObjectRef
	var retained map[ids.ObjectAddress]catalog.Descriptor
	add := func(ref ids.ObjectRef, d *catalog.Relation) error {
		address := ref.Address
		address.SubID = 0
		if address.ClassID != ids.RelationClassID || d != nil && address == d.Address() {
			return nil
		}
		if _, incumbent := stage.Base.Find(address); !incumbent {
			// Newly created providers belong to the private atomic candidate.
			provider, staged := stage.Get(address).(*catalog.Relation)
			if !staged || provider.Address() != address || ref.Domain() != stage.owner.catalog.Domain || ref.Epoch != stage.Base.Epoch() {
				return fmt.Errorf("stuntgres: invalid staged definition provider")
			}
			return nil
		}
		valid := stored.ReferenceEpochValid(stage.Base, d, ref)
		if d != nil && d.View != nil {
			// The stage's evolved consumer retains the incumbent definition's
			// immutable provenance; old epochs never become generic stage refs.
			incumbent, _ := stage.Base.Relation(d.Address())
			valid = incumbent != nil && incumbent.View == d.View && stored.ViewReferenceValid(stage.Base, incumbent, ref)
			if ref.Epoch == stage.Base.Epoch() {
				valid = stored.ViewReferenceValid(stage.Base, d, ref)
			}
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid retained definition provider")
		}
		// Prove the original attribute reference before using a whole-object lock.
		ref.Address = address
		if ref.Epoch != stage.Base.Epoch() {
			if retained == nil {
				retained = make(map[ids.ObjectAddress]catalog.Descriptor)
			}
			retained[ref.Address] = stage.Base.Get(ref.Address)
		}
		ref.Epoch = stage.Base.Epoch()
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		return nil
	}
	for _, descriptor := range stage.tableOrZero().likeSources {
		ref := stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), descriptor.Address())
		if err := add(ref, nil); err != nil {
			return err
		}
	}
	for _, descriptor := range stage.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
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
	e := s.engine()
	for _, ref := range refs {
		mode := lock.AccessShare
		for _, descriptor := range stage.Descriptors() {
			if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, ref.Address) {
				mode = lock.ShareUpdateExclusive
				break
			}
		}
		if err := s.acquireSequence(ref, mode, true); err != nil {
			return err
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err == nil {
			provider, ok := auth.Catalog.Relation(ref.Address)
			original, existed := stage.Base.Relation(ref.Address)
			if !ok && existed {
				// The provider existed when the definition was analyzed and was
				// dropped while this statement waited for it (PostgreSQL 18.6).
				err = sqlError("42704", "referenced relation was concurrently dropped")
			} else if !ok || !existed || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || provider.RelationKind != original.RelationKind || retained[ref.Address] != nil && provider != retained[ref.Address] {
				err = fmt.Errorf("stuntgres: definition provider disappeared before publication")
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	return stage.validateLikeSources(auth.Catalog)
}
