package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

var _ table.DropDependencyReader = (*dropProvider)(nil)

func (p *dropProvider) Context() context.Context { return p.ctx }
func (p *dropProvider) BaseRead() catalog.Read   { return p.base }
func (p *dropProvider) BaseRef(address ids.ObjectAddress) ids.ObjectRef {
	return p.owner.catalog.Domain.Ref(p.base.Epoch(), address)
}
func (p *dropProvider) StatementBound() bool { return p.scope != nil }
func (p *dropProvider) RecursingInheritance() bool {
	return p.scope != nil && p.scope.inheritance.alter != nil
}
