package engine

import (
	"slices"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Acquire retirement authority before discovering the dependent closure. A
// creator which already owns the provider's AccessShare lock can publish new
// AUTO members while DROP waits. Once granted, capture a fresh closure of the
// same object; never retry a lookup against a replacement or renamed object.
func admitDefinitionRetirement(s *core.StatementScope, stage *core.CatalogStage, stmt parser.Statement, input sqltype.InputEnvironment) (*core.CatalogStage, error) {
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
	slices.SortFunc(addresses, core.DefinitionAddressOrder)
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
