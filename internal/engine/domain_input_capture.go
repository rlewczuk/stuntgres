package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Input order is lexical (parameter/member order), not a later value-tree walk.
// This capture concerns only providers of the demanded input recipe, before
// leaving the binder's lock, and is discarded with that synchronous occurrence.
func captureDomainInputProviders(view catalog.Read, typ *sqltype.Descriptor) map[ids.ObjectAddress]catalog.Descriptor {
	providers := make(map[ids.ObjectAddress]catalog.Descriptor)
	var visit func(*sqltype.Descriptor)
	visit = func(typ *sqltype.Descriptor) {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		if _, visited := providers[address]; visited {
			return
		}
		if descriptor, exists := view.Descriptor(address); exists {
			providers[address] = descriptor
			if domain, ok := descriptor.(*catalog.Type); ok {
				for _, check := range domain.DomainChecks {
					for _, dependency := range check.Dependencies {
						address := dependency.Ref.Address
						address.SubID = 0
						if provider, exists := view.Descriptor(address); exists {
							if target, typed := provider.(*catalog.Type); typed {
								visit(target.SQLType)
							} else {
								providers[address] = provider
							}
						}
					}
				}
			}
		}
		if typ.IsDomain() {
			visit(typ.DomainBase().Type)
		}
		if typ.BaseType().IsArray() {
			visit(typ.BaseType().Element())
		}
		if typ.BaseType().IsComposite() {
			for i := range typ.BaseType().CompositeLen() {
				visit(typ.BaseType().CompositeMember(i).Usage.Type)
			}
		}
	}
	visit(typ)
	return providers
}

func validateCapturedInputBinding(captured, current catalog.Read, dependencies []catalog.ExpressionDependency) error {
	if captured.Owner() != current.Owner() || captured.Epoch() != current.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, dependency := range dependencies {
		address := dependency.Ref.Address
		address.SubID = 0
		provider, exists := captured.Descriptor(address)
		if !exists {
			continue
		} // Anonymous expression shapes are not catalog objects.
		if current.Get(address) != provider {
			return catalog.Conflict{}
		}
	}
	return nil
}
