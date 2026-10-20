package analyze

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *Binder) mutationTargetNamespace() QueryNamespace {
	columns := make([]QueryColumn, len(b.Target.Relation.Columns))
	for i, descriptor := range b.Target.Relation.Columns {
		columns[i] = QueryColumn{Descriptor: descriptor, Slot: i, Leaf: QueryTargetLeaf, Physical: i}
	}
	name := b.Target.Name
	if b.Target.alias != "" {
		name = b.Target.alias
	}
	qualifier := QueryQualifier{Name: name, Original: b.Target.Name, Alias: b.Target.alias, Ref: b.Target.Ref, Columns: columns, Schema: b.Query.Auth.ReadCatalog().Get(b.Target.Relation.Namespace()).Name(), LateralForbidden: true}
	return QueryNamespace{Columns: columns, Qualifiers: []QueryQualifier{qualifier}, Frame: b.Target.Relation.Columns}
}
