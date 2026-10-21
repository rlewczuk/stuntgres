package table

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const RestoreNoInheritBoundary = "restore accepts only paired canonical pg_constraint no-inherit repairs"

type RestoredNotNull struct {
	Ref        ids.ObjectRef
	Attribute  int32
	Constraint ids.OID
}

// The declaration supplies the exact captured name, even when another object
// occupies the usual generated stem. The temporary ordinary fact is repaired
// before any descendant declaration can inherit it. SET is genuine supported
// PostgreSQL syntax; only the narrowly paired catalog repair is importer-only.
func (t *DumpRelation) EmitNoInheritNotNull(emit func(string) error, setOwner func(string) error) error {
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	for _, constraint := range t.NotNullConstraints {
		if !constraint.NoInherit {
			continue
		}
		if constraint.NotValid {
			return fmt.Errorf("stuntgres: invalid no-inherit not-null replay fact")
		}
		column := t.Columns[t.Descriptor.IndexForAttribute(constraint.Attribute)].Name
		if err := setOwner(t.Owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(column) + " SET NOT NULL;\n"); err != nil {
			return err
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		literal := dump.CommentLiteral(qualified)
		repair := "UPDATE pg_catalog.pg_constraint SET connoinherit = true WHERE conrelid = " + literal + "::regclass AND conname = " + dump.CommentLiteral(constraint.Name) + " AND conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = " + literal + "::regclass AND attname = " + dump.CommentLiteral(column) + ")];\n"
		if err := emit(repair); err != nil {
			return err
		}
	}
	return nil
}

func RestoreNoInheritSyntax(raw *parser.Update) (parser.ObjectName, string, string, error) {
	reject := func() (parser.ObjectName, string, string, error) {
		return parser.ObjectName{}, "", "", sqlError("0A000", RestoreNoInheritBoundary)
	}
	if !RestoreCatalogName(raw.Name, "pg_constraint") || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) != 1 {
		return reject()
	}
	assignment := raw.Assignments[0]
	value, ok := assignment.Value.(parser.LiteralExpr)
	if assignment.Target.Text != "connoinherit" || len(assignment.Steps) != 0 || !ok || value.Value.Kind != parser.BooleanValue || !value.Value.Boolean {
		return reject()
	}
	outer, ok := raw.Where.(parser.LogicalExpr)
	if !ok || outer.Op != parser.LogicalAnd {
		return reject()
	}
	pair, ok := outer.Left.(parser.LogicalExpr)
	if !ok || pair.Op != parser.LogicalAnd {
		return reject()
	}
	relation, ok := restoreAttributeRankEquality(pair.Left, "conrelid")
	if !ok {
		return reject()
	}
	name, text, ok := restoreNoInheritRegclass(relation)
	if !ok {
		return reject()
	}
	label, ok := restoreAttributeRankEquality(pair.Right, "conname")
	if !ok {
		return reject()
	}
	literal, ok := label.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return reject()
	}
	key, ok := restoreAttributeRankEquality(outer.Right, "conkey")
	if !ok {
		return reject()
	}
	array, ok := key.(parser.ArrayExpr)
	if !ok || len(array.Elements) != 1 {
		return reject()
	}
	column, ok := restoreConstraintAttribute(array.Elements[0], text)
	if !ok {
		return reject()
	}
	return name, column, literal.Value.Text, nil
}

func restoreConstraintAttribute(raw parser.Expr, relation string) (string, bool) {
	subquery, ok := raw.(parser.SubqueryExpr)
	if !ok || subquery.Kind != parser.SubqueryScalar || subquery.Left != nil || subquery.Negate || len(subquery.Operator.Parts) != 0 || subquery.Query == nil {
		return "", false
	}
	query := subquery.Query
	if len(query.With) != 0 || query.WithRecursive || query.Values != nil || len(query.Targets) != 1 || query.GroupBy != nil || query.Having != nil || len(query.Windows) != 0 || query.Order != nil || query.Into != nil || len(query.Locking) != 0 || query.Bounds != nil || query.Distinct != nil || query.Set != nil {
		return "", false
	}
	for _, label := range query.Labels {
		if label.Text != "" {
			return "", false
		}
	}
	from, ok := query.From.(*parser.TableReference)
	if !ok || from.Only || !RestoreCatalogName(from.Name, "pg_attribute") {
		return "", false
	}
	target, ok := query.Targets[0].(parser.ColumnExpr)
	if !ok || len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "attnum" {
		return "", false
	}
	attribute, ok := query.Where.(parser.LogicalExpr)
	if !ok || attribute.Op != parser.LogicalAnd {
		return "", false
	}
	attributeRelation, ok := restoreAttributeRankEquality(attribute.Left, "attrelid")
	if !ok {
		return "", false
	}
	_, attributeText, ok := restoreNoInheritRegclass(attributeRelation)
	if !ok || attributeText != relation {
		return "", false
	}
	column, ok := restoreAttributeRankEquality(attribute.Right, "attname")
	if !ok {
		return "", false
	}
	literal, ok := column.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return "", false
	}
	return literal.Value.Text, true
}

func RestoreCatalogName(name parser.ObjectName, relation string) bool {
	return len(name.Parts) == 2 && name.Parts[0].Text == "pg_catalog" && name.Parts[1].Text == relation
}

func restoreNoInheritRegclass(raw parser.Expr) (parser.ObjectName, string, bool) {
	cast, ok := raw.(parser.RegclassExpr)
	if !ok {
		return parser.ObjectName{}, "", false
	}
	literal, ok := cast.Child.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return parser.ObjectName{}, "", false
	}
	name, err := parser.ParseRegclassName(literal.Value.Text)
	return name, literal.Value.Text, err == nil && len(name.Parts) == 2
}
