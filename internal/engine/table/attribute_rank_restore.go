package table

import (
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const RestoreAttributeRankBoundary = "restore accepts only canonical pg_attribute rank repairs"

func RestoreAttributeRankSyntax(raw *parser.Update) (parser.ObjectName, string, int16, error) {
	reject := func() (parser.ObjectName, string, int16, error) {
		return parser.ObjectName{}, "", 0, sqlError("0A000", RestoreAttributeRankBoundary)
	}
	if len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "pg_catalog" || raw.Name.Parts[1].Text != "pg_attribute" || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) != 1 {
		return reject()
	}
	assignment := raw.Assignments[0]
	if assignment.Target.Text != "attndims" || len(assignment.Steps) != 0 {
		return reject()
	}
	value, ok := assignment.Value.(parser.LiteralExpr)
	if !ok || value.Value.Kind != parser.IntegerValue {
		return reject()
	}
	if value.Value.Integer < 0 || value.Value.Integer > math.MaxInt16 {
		return parser.ObjectName{}, "", 0, sqlError("22003", "attribute rank is out of range")
	}
	where, ok := raw.Where.(parser.LogicalExpr)
	if !ok || where.Op != parser.LogicalAnd {
		return reject()
	}
	relation, ok := restoreAttributeRankEquality(where.Left, "attrelid")
	if !ok {
		return reject()
	}
	cast, ok := relation.(parser.RegclassExpr)
	if !ok {
		return reject()
	}
	text, ok := cast.Child.(parser.LiteralExpr)
	if !ok || text.Value.Kind != parser.StringValue {
		return reject()
	}
	name, err := parser.ParseRegclassName(text.Value.Text)
	if err != nil || len(name.Parts) != 2 {
		return reject()
	}
	label, ok := restoreAttributeRankEquality(where.Right, "attname")
	if !ok {
		return reject()
	}
	literal, ok := label.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return reject()
	}
	return name, literal.Value.Text, int16(value.Value.Integer), nil
}

func restoreAttributeRankEquality(raw parser.Expr, column string) (parser.Expr, bool) {
	comparison, ok := raw.(parser.OperatorExpr)
	if !ok || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != "=" || len(comparison.Args) != 2 {
		return nil, false
	}
	left, ok := comparison.Args[0].(parser.ColumnExpr)
	if !ok || len(left.Name.Parts) != 1 || left.Name.Parts[0].Text != column {
		return nil, false
	}
	return comparison.Args[1], true
}

// AttributeRankCandidate returns ref's table with the declared array rank of
// column set to rank; nil when it already has it. Only an array column may
// take a nonzero rank.
func AttributeRankCandidate(view catalog.Read, ref ids.ObjectRef, column string, rank int16) (*catalog.Relation, error) {
	old, ok := view.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation {
		return nil, sqlError("0A000", RestoreAttributeRankBoundary)
	}
	slot, exists := old.Index[column]
	if !exists {
		return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, old.Name()))
	}
	if rank != 0 && !old.Columns[slot].Usage.Type.IsArray() {
		return nil, sqlError("0A000", RestoreAttributeRankBoundary)
	}
	if old.Columns[slot].DeclaredDimensions == rank {
		return nil, nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Columns = slices.Clone(old.Columns)
	next.Columns[slot].DeclaredDimensions = rank
	return &next, nil
}
