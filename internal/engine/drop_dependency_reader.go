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
	_ table.DropDependencyReader = (*DropProvider)(nil)
	_ view.DropReader            = (*DropProvider)(nil)
)

func (p *DropProvider) Context() context.Context { return p.Ctx }
func (p *DropProvider) BaseRead() catalog.Read   { return p.Base }
func (p *DropProvider) BaseRef(address ids.ObjectAddress) ids.ObjectRef {
	return p.Owner.Catalog.Domain.Ref(p.Base.Epoch(), address)
}

// DependencyClosure returns the dependents of targets in the drop.
func (p *DropProvider) DependencyClosure(targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	return view.DropClosure(p, targets)
}

// Path is the SQL lookup path of the drop's diagnostics.
func (p *DropProvider) Path() []ids.ObjectAddress { return p.SearchPath }

// DefinitionInput is the preparation of a prepared drop, the stage of a staged
// one, or nil for a bare provider.
func (p *DropProvider) DefinitionInput() catalog.DefinitionReader {
	if p.Prepared != nil {
		return p.Prepared
	}
	if p.Stage != nil {
		return StageDefinitions(p.Stage)
	}
	return nil
}
func (p *DropProvider) StatementBound() bool { return p.Scope != nil }

// TypedFieldCheckOwner reports whether the stage's admitted typed closure owns
// relation's inherited CHECKs; a prepared or bare drop owns none.
func (p *DropProvider) TypedFieldCheckOwner(relation *catalog.Relation) bool {
	return p.Stage != nil && typedef.TypedFieldCheckOwner(p.Stage.Typed, p.Stage.Base, relation)
}
func (p *DropProvider) RecursingInheritance() bool {
	return p.Scope != nil && p.Scope.Inheritance.Alter != nil
}
