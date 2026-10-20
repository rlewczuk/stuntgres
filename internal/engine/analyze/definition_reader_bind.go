package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (b *Binder) DefinitionReader() catalog.DefinitionReader {
	return b.Host.BindingDefinition(b.Query.Auth)
}

func (b *Binder) hasSQLProcedureName(name string) bool {
	if reader := b.DefinitionReader(); reader != nil {
		return reader.HasSQLProcedureName(name)
	}
	return b.BindingCatalog().State().HasSQLProcedureName(name)
}

func (b *Binder) definitionDescriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if reader := b.DefinitionReader(); reader != nil {
		return reader.Descriptor(address)
	}
	return b.BindingCatalog().Descriptor(address)
}

func (b *Binder) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(b.BindingCatalog(), b.DefinitionReader(), id)
}

func (b *Binder) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(b.BindingCatalog(), b.DefinitionReader(), element)
}

// The authority* reads go through auth's own catalog and definition reader,
// which the host supplies (a binding authority reads through the operation's).
func (b *Binder) authorityDescriptor(auth Authority, address ids.ObjectAddress) (catalog.Descriptor, bool) {
	return catalog.DescriptorAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), address)
}

func (b *Binder) authorityRelation(auth Authority, address ids.ObjectAddress) (*catalog.Relation, bool) {
	return catalog.DefinitionRelationAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), address)
}

func (b *Binder) authorityNamespace(auth Authority, address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return catalog.DefinitionNamespaceAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), address)
}

func (b *Binder) authorityType(auth Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), id)
}
