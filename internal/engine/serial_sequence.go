package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// evalSerialSequence reads the statement's catalog, not defaults or generator
// state. AUTO (serial/manual) and INTERNAL (identity) share the same owned address.
func (e deparseExpression) evalSerialSequence(c *EvalContext, values [3]sqltype.Value) (sqltype.Value, error) {
	scope := c.liveScope()
	catalog := scope.commentCatalog
	table, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	column, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	name, err := parser.ParseRegclassName(table)
	if err != nil {
		return sqltype.Value{}, sqlError("42602", "invalid name syntax")
	}
	database := ""
	if scope.deparseAuth.session != nil {
		database = scope.deparseAuth.session.databaseName
	}
	name, err = regclassQualifiedName(name, table, database)
	if err != nil {
		return sqltype.Value{}, err
	}
	for i := range name.Parts {
		// These offsets belong to TEXT input, not the outer SQL statement.
		name.Parts[i].Pos = parser.Position{}
	}
	ref, err := catalog.owner.resolveRelationSnapshot(scope.deparseAuth, name, true)
	if err != nil {
		return sqltype.Value{}, err
	}
	relation := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	owner := ref.Address
	for i, descriptor := range relation.columns {
		if descriptor.Name == column {
			owner.SubID = relation.attributes[i]
			break
		}
	}
	if owner.SubID == 0 {
		if isSystemColumn(column) {
			switch relation.relationKind {
			case tableRelation, sequenceRelation, catalogRelation, indexRelation:
				return sqltype.Null(e.Usage.Type), nil
			}
		}
		return sqltype.Value{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, relation.name))
	}
	for _, descriptor := range catalog.state.descriptors {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		sequence, ok := descriptor.(*relationDescriptor)
		if !ok || sequence.relationKind != sequenceRelation || sequence.sequence == nil || sequence.sequence.owned != owner {
			continue
		}
		namespace, ok := catalog.state.descriptors[sequence.namespace].(*namespaceDescriptor)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing owned sequence namespace")
		}
		return sqltype.String(e.Usage.Type, pgIdentifier(namespace.name)+"."+pgIdentifier(sequence.name)), nil
	}
	return sqltype.Null(e.Usage.Type), nil
}
