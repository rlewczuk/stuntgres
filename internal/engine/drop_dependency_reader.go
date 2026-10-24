package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
)

var (
	_ table.DropDependencyReader = (*dropProvider)(nil)
	_ view.DropReader            = (*dropProvider)(nil)
)

func (p *dropProvider) Context() context.Context { return p.ctx }
func (p *dropProvider) BaseRead() catalog.Read   { return p.base }
func (p *dropProvider) BaseRef(address ids.ObjectAddress) ids.ObjectRef {
	return p.owner.catalog.Domain.Ref(p.base.Epoch(), address)
}

// DependencyClosure returns the dependents of targets in the drop.
func (p *dropProvider) DependencyClosure(targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	return view.DropClosure(p, targets)
}

// Path is the SQL lookup path of the drop's diagnostics.
func (p *dropProvider) Path() []ids.ObjectAddress { return p.path }

// DefinitionInput is the preparation of a prepared drop, the stage of a staged
// one, or nil for a bare provider.
func (p *dropProvider) DefinitionInput() catalog.DefinitionReader {
	if p.prepared != nil {
		return p.prepared
	}
	if p.stage != nil {
		return stageDefinitions(p.stage)
	}
	return nil
}
func (p *dropProvider) StatementBound() bool { return p.scope != nil }

// TypedFieldCheckOwner reports whether the stage's admitted typed closure owns
// relation's inherited CHECKs; a prepared or bare drop owns none.
func (p *dropProvider) TypedFieldCheckOwner(relation *catalog.Relation) bool {
	return p.stage != nil && typedef.TypedFieldCheckOwner(p.stage.typed, p.stage.Base, relation)
}
func (p *dropProvider) RecursingInheritance() bool {
	return p.scope != nil && p.scope.inheritance.alter != nil
}
