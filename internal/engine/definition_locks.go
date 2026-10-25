package engine

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func definitionEndpoint(d catalog.Descriptor) bool {
	switch d := d.(type) {
	case *catalog.AccessMethod:
		return !catalog.AccessMethodSeeded(d)
	case *catalog.OperatorFamily:
		return !catalog.OperatorFamilySeeded(d)
	case *catalog.OperatorClass:
		return !catalog.OperatorClassSeeded(d)
	}
	return false
}

func DefinitionAddressOrder(a, b ids.ObjectAddress) int {
	if n := cmp.Compare(a.ClassID, b.ClassID); n != 0 {
		return n
	}
	return cmp.Compare(a.ObjectID, b.ObjectID)
}

// Dependency records hold the actual non-pinned endpoint, never a method-wide
// default-class absence key. This runs outside Engine.mu and shares the normal
// transaction ticket, cancellation and savepoint ownership of catalog locks.
func (s *StatementScope) acquireDefinitionDependencyLocks(stage *CatalogStage) error {
	var scratch [8]ids.ObjectAddress
	addresses := scratch[:0]
	for _, d := range stage.Descriptors() {
		if err := visitDefinitionProviders(stage.Base, d, func(a ids.ObjectAddress) error {
			if definitionEndpoint(stage.Base.Get(a)) && !slices.Contains(addresses, a) {
				addresses = append(addresses, a)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	slices.SortFunc(addresses, DefinitionAddressOrder)
	for _, a := range addresses {
		ref := s.Engine().Catalog.Domain.Ref(stage.Base.Epoch(), a)
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
			return err
		}
	}
	if len(addresses) == 0 {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	if current.Catalog.Epoch() != stage.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, a := range addresses {
		actual := current.Catalog.Get(a)
		if actual == nil {
			kind := "operator class"
			switch a.ClassID {
			case ids.AccessMethodClassID:
				kind = "access method"
			case ids.OperatorFamilyClassID:
				kind = "operator family"
			}
			return SQLError("42704", "referenced "+kind+" was concurrently dropped")
		}
		if actual != stage.Base.Get(a) {
			return catalog.Conflict{}
		}
	}
	return nil
}

// Acquire retirement authority before discovering the dependent closure. A
// creator which already owns the provider's AccessShare lock can publish new
// AUTO members while DROP waits. Once granted, capture a fresh closure of the
// same object; never retry a lookup against a replacement or renamed object.
func admitDefinitionRetirement(s *StatementScope, stage *CatalogStage, stmt parser.Statement, input sqltype.InputEnvironment) (*CatalogStage, error) {
	switch stmt.(type) {
	case *parser.DropAccessMethod, *parser.DropOperatorFamily, *parser.DropOperatorClass:
	default:
		return stage, nil
	}
	if len(stage.DomainTargets) == 0 {
		return stage, nil
	}
	var scratch [4]ids.ObjectAddress
	addresses := append(scratch[:0], stage.DomainTargets...)
	slices.SortFunc(addresses, DefinitionAddressOrder)
	e := s.Engine()
	for _, a := range addresses {
		ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), a)
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			return stage, err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return stage, err
	}
	if current.Catalog.Epoch() != stage.Base.Epoch() {
		return stage, ids.ErrCatalogReplaced
	}
	for _, a := range addresses {
		if current.Catalog.Get(a) != stage.Base.Get(a) {
			return stage, ids.ErrCatalogReplaced
		}
	}
	fresh, err := stageSchemaLocked(e, s.Op, stmt, input, s.Output)
	if err != nil {
		return stage, err
	}
	// stageSchemaLocked owns current authority and captures the now-complete
	// provider graph. The old stage has not yet expanded or mutated any closure.
	e.DiscardCatalogStage(stage)
	return fresh, nil
}
