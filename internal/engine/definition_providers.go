package engine

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
)

// Persistent definition holds use the incumbent ticket and replacement fence.
// Admission occurs outside Engine.mu; after a wait identities are opened anew,
// never retargeted by name or evaluated to establish a dependency.
func (s *StatementScope) AcquireDefinitionProviders(stage *CatalogStage) error {
	if err := s.acquireForeignKeyDefinitionProviders(stage); err != nil {
		return err
	}
	requests, retained, err := stage.TableSlot.DefinitionProviders(stage.Base, stage, stage.Descriptors(), stage.Owner.Catalog.Domain)
	if err != nil {
		return err
	}
	e := s.Engine()
	for _, request := range requests {
		ref := request.Ref
		if err := s.AcquireSequence(ref, request.Mode, true); err != nil {
			return err
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err == nil {
			provider, ok := auth.Catalog.Relation(ref.Address)
			original, existed := stage.Base.Relation(ref.Address)
			if !ok && existed {
				// The provider existed when the definition was analyzed and was
				// dropped while this statement waited for it (PostgreSQL 18.6).
				err = SQLError("42704", "referenced relation was concurrently dropped")
			} else if !ok || !existed || ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || provider.RelationKind != original.RelationKind || retained[ref.Address] != nil && provider != retained[ref.Address] {
				err = fmt.Errorf("stuntgres: definition provider disappeared before publication")
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	return stage.validateLikeSources(auth.Catalog)
}

// Every actual removed procedure, including a cascade descendant, uses the
// same ordinary object ticket as readers of a checked routine definition.
func (s *StatementScope) acquireRoutineRetirements(stage *CatalogStage) error {
	var scratch [8]ids.ObjectAddress
	addresses := scratch[:0]
	for address := range stage.Removals() {
		if address.ClassID == ids.ProcedureClassID {
			addresses = append(addresses, address)
		}
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	for _, address := range addresses {
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: stage.Base.Domain().Ref(stage.Base.Epoch(), address)}, lock.AccessExclusive); err != nil {
			return err
		}
	}
	return nil
}

// Consume the canonical checked manifests, never quoted source or opened
// producers. Sources are stage read inputs; descriptors/programs remain the
// sole executable authority and the journal captures their ordinary fences.
func (s *StatementScope) AcquireRoutineDefinitionProviders(stage *CatalogStage) error {
	var scratch [8]catalog.Descriptor
	sources := scratch[:0]
	add := func(address ids.ObjectAddress) bool {
		address.SubID = 0
		if address.ClassID != ids.ProcedureClassID && address.ClassID != ids.TypeClassID && address.ClassID != ids.RelationClassID {
			return true
		}
		source := stage.Base.Get(address)
		if source != nil && !slices.ContainsFunc(sources, func(old catalog.Descriptor) bool { return old.Address() == address }) {
			sources = append(sources, source)
		}
		return true
	}
	for address, descriptor := range stage.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Procedure:
			old, _ := stage.Base.Procedure(address)
			if old == nil || old.SQL != d.SQL || old.SQLBody != d.SQLBody || old.SQLDefaults != d.SQLDefaults || old.Aggregate != d.Aggregate {
				catalog.VisitProcedureDefinitionDependencies(d, add)
			}
		case *catalog.Operator:
			old, _ := stage.Base.Operator(address)
			if old == nil || old.Function != d.Function || old.Restriction != d.Restriction || old.Join != d.Join {
				routine.VisitOperatorDependencyAddresses(stage.Base, d, add)
			}
		case *catalog.Relation:
			definition := stored.ViewOf(d)
			old, _ := stage.Base.Relation(address)
			if definition != nil && (old == nil || stored.ViewOf(old) != definition) {
				// A checked view retains genuine routine identities just like a
				// checked routine body. Relation sources already have their
				// ordinary view-definition locks; do not expand this to types,
				// names, or soft quoted-source references.
				for _, dependency := range definition.Dependencies() {
					if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
						add(dependency.Ref.Address)
					}
				}
			}
		}
	}
	slices.SortFunc(sources, func(a, b catalog.Descriptor) int {
		if n := cmp.Compare(a.Address().ClassID, b.Address().ClassID); n != 0 {
			return n
		}
		return cmp.Compare(a.Address().ObjectID, b.Address().ObjectID)
	})
	for _, source := range sources {
		address := source.Address()
		if address.ClassID == ids.ProcedureClassID || address.ClassID == ids.RelationClassID {
			if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: stage.Base.Domain().Ref(stage.Base.Epoch(), address)}, lock.AccessShare); err != nil {
				return err
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil && auth.Catalog.Epoch() != stage.Base.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	for _, source := range sources {
		if err != nil {
			break
		}
		current := auth.Catalog.Get(source.Address())
		if current == nil && source.Address().ClassID == ids.ProcedureClassID {
			err = SQLError("42704", "referenced function was concurrently dropped")
		} else if current == nil && source.Address().ClassID == ids.RelationClassID {
			err = SQLError("42704", "referenced relation was concurrently dropped")
		} else if current != source {
			err = catalog.Conflict{}
		}
	}
	if err == nil && (auth.Revision != stage.AuthRevision || auth.RoleRecord.Revision != stage.RoleRevision) {
		err = catalog.Conflict{}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	stage.definitionSources = append(stage.definitionSources, sources...)
	return nil
}
