package engine

import "stuntgres/internal/engine/analyze"

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *expressionBinder) mutationTargetNamespace() queryNamespace {
	columns := make([]queryColumn, len(b.Target.Relation.Columns))
	for i, descriptor := range b.Target.Relation.Columns {
		columns[i] = queryColumn{Descriptor: descriptor, Slot: i, Leaf: analyze.QueryTargetLeaf, Physical: i}
	}
	name := b.Target.Name
	if b.Target.alias != "" {
		name = b.Target.alias
	}
	qualifier := queryQualifier{Name: name, Original: b.Target.Name, Alias: b.Target.alias, Ref: b.Target.Ref, Columns: columns, Schema: b.Query.Auth.ReadCatalog().Get(b.Target.Relation.Namespace()).Name(), LateralForbidden: true}
	return queryNamespace{Columns: columns, Qualifiers: []queryQualifier{qualifier}, Frame: b.Target.Relation.Columns}
}
