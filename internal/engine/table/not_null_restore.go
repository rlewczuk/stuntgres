package table

import (
	"fmt"
	"slices"
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

// CaptureRestoredNotNulls records, in origins, the explicitly named, valid,
// purely local NOT NULL constraints raw declared on the relation Restore just
// created as created; a later SET NOT NULL repair pairs with them.
func CaptureRestoredNotNulls(view catalog.Read, created ids.ObjectRef, raw *parser.CreateTable, origins map[ids.OID]RestoredNotNull) {
	relation, ok := view.Relation(created.Address)
	if !ok {
		return
	}
	capture := func(column, name string) {
		if name == "" {
			return
		}
		slot, ok := relation.Index[column]
		if !ok {
			return
		}
		attribute := relation.Attributes[slot]
		for _, constraint := range relation.Constraints.NotNull {
			if constraint.Name == name && constraint.Attribute == attribute && !constraint.NotValid {
				if graph := relation.Inheritance; graph != nil {
					fact := graph.NotNull[attribute]
					if !fact.Local || fact.Count() != 0 {
						return
					}
				}
				origins[constraint.OID] = RestoredNotNull{Ref: created, Attribute: attribute, Constraint: constraint.OID}
				return
			}
		}
	}
	for _, element := range raw.Elements {
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.NotNull {
					capture(element.Column.Name, constraint.Name.Text)
				}
			}
		}
		if element.NotNull != nil {
			capture(element.NotNull.Column.Text, element.NotNull.Name.Text)
		}
	}
}

// RestoredNotNullShape reports whether raw has the one shape a paired NOT
// NULL takes: ALTER TABLE ONLY schema.table ALTER COLUMN ... SET NOT NULL.
func RestoredNotNullShape(raw *parser.AlterTable) bool {
	return raw.Only && !raw.IfExists && len(raw.Name.Parts) == 2 && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.SetNotNull
}

// PairRestoredNotNull returns the captured origin of the valid NOT NULL the
// SET NOT NULL of raw names on ref.
func PairRestoredNotNull(view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable, origins map[ids.OID]RestoredNotNull) (RestoredNotNull, error) {
	relation, ok := view.Relation(ref.Address)
	if !ok {
		return RestoredNotNull{}, sqlError("0A000", RestoreNoInheritBoundary)
	}
	slot, ok := relation.Index[raw.Actions[0].Column.Text]
	if !ok {
		return RestoredNotNull{}, sqlError("0A000", RestoreNoInheritBoundary)
	}
	for _, constraint := range relation.Constraints.NotNull {
		if constraint.Attribute != relation.Attributes[slot] || constraint.NotValid {
			continue
		}
		origin, exists := origins[constraint.OID]
		if exists && origin.Ref == ref && origin.Attribute == constraint.Attribute {
			return origin, nil
		}
	}
	return RestoredNotNull{}, sqlError("0A000", RestoreNoInheritBoundary)
}

// NoInheritNotNullCandidate returns ref's relation with the paired NOT NULL
// origin of column, named label, marked NO INHERIT; nil when it already is.
// The constraint must still be the captured, valid, purely local one, and no
// primary key may cover its column.
func NoInheritNotNullCandidate(view catalog.Read, ref ids.ObjectRef, origin RestoredNotNull, column, label string) (*catalog.Relation, error) {
	reject := func() (*catalog.Relation, error) { return nil, sqlError("0A000", RestoreNoInheritBoundary) }
	old, ok := view.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation || origin.Ref != ref {
		return reject()
	}
	slot, ok := old.Index[column]
	if !ok || old.Attributes[slot] != origin.Attribute || old.Columns[slot].Nullable {
		return reject()
	}
	index := slices.IndexFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
		return c.OID == origin.Constraint && c.Name == label && c.Attribute == origin.Attribute && !c.NotValid
	})
	if index < 0 {
		return reject()
	}
	for _, constraint := range old.Constraints.Unique {
		if constraint.Primary && slices.Contains(constraint.Attributes, origin.Attribute) {
			return reject()
		}
	}
	if graph := old.Inheritance; graph != nil {
		fact := graph.NotNull[origin.Attribute]
		if !fact.Local || fact.Count() != 0 {
			return reject()
		}
	}
	if old.Constraints.NotNull[index].NoInherit {
		return nil, nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	next.Constraints.NotNull[index].NoInherit = true
	return &next, nil
}
