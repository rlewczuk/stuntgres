package engine

import "stuntgres/internal/engine/analyze"

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *expressionBinder) mutationTargetNamespace() queryNamespace {
	columns := make([]queryColumn, len(b.target.relation.Columns))
	for i, descriptor := range b.target.relation.Columns {
		columns[i] = queryColumn{Descriptor: descriptor, Slot: i, Leaf: analyze.QueryTargetLeaf, Physical: i}
	}
	name := b.target.name
	if b.target.alias != "" {
		name = b.target.alias
	}
	qualifier := queryQualifier{Name: name, Original: b.target.name, Alias: b.target.alias, Ref: b.target.ref, Columns: columns, Schema: b.query.auth.ReadCatalog().Get(b.target.relation.Namespace()).Name(), LateralForbidden: true}
	return queryNamespace{Columns: columns, Qualifiers: []queryQualifier{qualifier}, Frame: b.target.relation.Columns}
}
