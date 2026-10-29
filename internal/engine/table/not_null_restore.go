package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

const RestoreConstraintModeBoundary = "restore accepts only unconditional qualified single NO INHERIT constraint alterations"

// Valid noninheritable facts must be altered before descendants are declared.
// Invalid facts are constructed after data and altered by EmitInvalidConstraints.
func (t *DumpRelation) EmitNoInheritNotNull(emit func(string) error, setOwner func(string) error) error {
	for _, constraint := range t.NotNullConstraints {
		if !constraint.NoInherit || constraint.NotValid {
			continue
		}
		if err := setOwner(t.Owner); err != nil {
			return err
		}
		if err := t.emitNoInheritNotNull(constraint.Name, emit); err != nil {
			return err
		}
	}
	return nil
}

func (t *DumpRelation) emitNoInheritNotNull(name string, emit func(string) error) error {
	return emit("ALTER TABLE ONLY " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ALTER CONSTRAINT " + catalog.QuoteIdentifier(name) + " NO INHERIT;\n")
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
