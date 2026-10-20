package engine

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *expressionBinder) mutationTargetNamespace() queryNamespace {
	columns := make([]queryColumn, len(b.target.relation.Columns))
	for i, descriptor := range b.target.relation.Columns {
		columns[i] = queryColumn{descriptor: descriptor, slot: i, leaf: queryTargetLeaf, physical: i}
	}
	name := b.target.name
	if b.target.alias != "" {
		name = b.target.alias
	}
	qualifier := queryQualifier{name: name, original: b.target.name, alias: b.target.alias, ref: b.target.ref, columns: columns, schema: b.query.auth.ReadCatalog().Get(b.target.relation.Namespace()).Name(), lateralForbidden: true}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: b.target.relation.Columns}
}
