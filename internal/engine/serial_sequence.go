package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// evalSerialSequence reads the statement's catalog, not defaults or generator
// state. AUTO (serial/manual) and INTERNAL (identity) share the same owned address.
func evalSerialSequence(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := scopeOf(c.LiveHost())
	view := scope.inquiry.commentCatalog
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
	if scope.inquiry.deparseAuth.session != nil {
		database = scope.inquiry.deparseAuth.session.databaseName
	}
	name, err = regclassQualifiedName(name, table, database)
	if err != nil {
		return sqltype.Value{}, err
	}
	for i := range name.Parts {
		// These offsets belong to TEXT input, not the outer SQL statement.
		name.Parts[i].Pos = parser.Position{}
	}
	ref, err := scope.inquiry.deparseAuth.engine.resolveRelationSnapshot(scope.inquiry.deparseAuth, name, true)
	if err != nil {
		return sqltype.Value{}, err
	}
	relation := view.MustRelation(ref.Address)
	owner := ref.Address
	for i, descriptor := range relation.Columns {
		if descriptor.Name == column {
			owner.SubID = relation.Attributes[i]
			break
		}
	}
	if owner.SubID == 0 {
		if isSystemColumn(column) {
			switch relation.RelationKind {
			case catalog.TableRelation, catalog.SequenceRelation, catalog.CatalogRelation, catalog.IndexRelation:
				return sqltype.Null(result), nil
			}
		}
		return sqltype.Value{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, relation.Name()))
	}
	for _, descriptor := range view.Descriptors() {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		sequence, ok := descriptor.(*catalog.Relation)
		if !ok || sequence.RelationKind != catalog.SequenceRelation || sequence.Sequence == nil || sequence.Sequence.Owned != owner {
			continue
		}
		namespace, ok := view.Namespace(sequence.Namespace())
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing owned sequence namespace")
		}
		return sqltype.String(result, pgIdentifier(namespace.Name())+"."+pgIdentifier(sequence.Name())), nil
	}
	return sqltype.Null(result), nil
}
