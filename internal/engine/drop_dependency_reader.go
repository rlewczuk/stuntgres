package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var _ dropDependencyReader = (*dropProvider)(nil)

func (p *dropProvider) context() context.Context { return p.ctx }
func (p *dropProvider) baseRead() catalog.Read   { return p.base }
func (p *dropProvider) baseRef(address ids.ObjectAddress) ids.ObjectRef {
	return p.owner.catalog.Domain.Ref(p.base.Epoch(), address)
}
func (p *dropProvider) statementBound() bool { return p.scope != nil }
func (p *dropProvider) recursingInheritance() bool {
	return p.scope != nil && p.scope.inheritance.alter != nil
}
