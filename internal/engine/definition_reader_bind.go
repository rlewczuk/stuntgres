package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) definitionReader() catalog.DefinitionReader {
	return b.host.BindingDefinition(b.query.auth)
}

func (b *expressionBinder) hasSQLProcedureName(name string) bool {
	if reader := b.definitionReader(); reader != nil {
		return reader.HasSQLProcedureName(name)
	}
	return b.bindingCatalog().State().HasSQLProcedureName(name)
}

func (b *expressionBinder) definitionDescriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if reader := b.definitionReader(); reader != nil {
		return reader.Descriptor(address)
	}
	return b.bindingCatalog().Descriptor(address)
}

func (b *expressionBinder) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(b.bindingCatalog(), b.definitionReader(), id)
}

func (b *expressionBinder) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(b.bindingCatalog(), b.definitionReader(), element)
}

// The authority* reads go through auth's own catalog and definition reader,
// which the host supplies (a binding authority reads through the operation's).
func (b *expressionBinder) authorityDescriptor(auth analyze.Authority, address ids.ObjectAddress) (catalog.Descriptor, bool) {
	return catalog.DescriptorAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}

func (b *expressionBinder) authorityRelation(auth analyze.Authority, address ids.ObjectAddress) (*catalog.Relation, bool) {
	return catalog.DefinitionRelationAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}

func (b *expressionBinder) authorityNamespace(auth analyze.Authority, address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return catalog.DefinitionNamespaceAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}

func (b *expressionBinder) authorityType(auth analyze.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), id)
}
