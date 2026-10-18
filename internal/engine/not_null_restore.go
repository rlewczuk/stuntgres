package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const restoreNoInheritBoundary = "restore accepts only paired canonical pg_constraint no-inherit repairs"

type restoredNotNull struct {
	ref        ids.ObjectRef
	attribute  int32
	constraint ids.OID
}

// The declaration supplies the exact captured name, even when another object
// occupies the usual generated stem. The temporary ordinary fact is repaired
// before any descendant declaration can inherit it. SET is genuine supported
// PostgreSQL syntax; only the narrowly paired catalog repair is importer-only.
func (t *dumpRelation) emitNoInheritNotNull(emit func(string) error, setOwner func(string) error) error {
	qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
	for _, constraint := range t.notNullConstraints {
		if !constraint.NoInherit {
			continue
		}
		if constraint.NotValid {
			return fmt.Errorf("stuntgres: invalid no-inherit not-null replay fact")
		}
		column := t.columns[t.descriptor.indexForAttribute(constraint.Attribute)].Name
		if err := setOwner(t.owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + quoteIdentifier(column) + " SET NOT NULL;\n"); err != nil {
			return err
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		literal := commentLiteral(qualified)
		repair := "UPDATE pg_catalog.pg_constraint SET connoinherit = true WHERE conrelid = " + literal + "::regclass AND conname = " + commentLiteral(constraint.Name) + " AND conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = " + literal + "::regclass AND attname = " + commentLiteral(column) + ")];\n"
		if err := emit(repair); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) captureRestoredNotNulls(created ids.ObjectRef, raw *parser.CreateTable, origins map[ids.OID]restoredNotNull) {
	relation, ok := e.CatalogRead().state.descriptors[created.Address].(*relationDescriptor)
	if !ok {
		return
	}
	capture := func(column, name string) {
		if name == "" {
			return
		}
		slot, ok := relation.index[column]
		if !ok {
			return
		}
		attribute := relation.attributes[slot]
		for _, constraint := range relation.notNullConstraints {
			if constraint.Name == name && constraint.Attribute == attribute && !constraint.NotValid {
				if graph := relation.inheritance; graph != nil {
					fact := graph.notNull[attribute]
					if !fact.local || fact.count() != 0 {
						return
					}
				}
				origins[constraint.OID] = restoredNotNull{created, attribute, constraint.OID}
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

func (e *Engine) pairRestoredNotNull(op *Operation, raw *parser.AlterTable, origins map[ids.OID]restoredNotNull) (restoredNotNull, error) {
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.SetNotNull {
		return restoredNotNull{}, sqlError("0A000", restoreNoInheritBoundary)
	}
	auth, err := op.Authorization()
	if err != nil {
		return restoredNotNull{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return restoredNotNull{}, err
	}
	relation, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return restoredNotNull{}, sqlError("0A000", restoreNoInheritBoundary)
	}
	slot, ok := relation.index[raw.Actions[0].Column.Text]
	if !ok {
		return restoredNotNull{}, sqlError("0A000", restoreNoInheritBoundary)
	}
	for _, constraint := range relation.notNullConstraints {
		if constraint.Attribute != relation.attributes[slot] || constraint.NotValid {
			continue
		}
		origin, exists := origins[constraint.OID]
		if exists && origin.ref == ref && origin.attribute == constraint.Attribute {
			return origin, nil
		}
	}
	return restoredNotNull{}, sqlError("0A000", restoreNoInheritBoundary)
}

// Unlike public UPDATE, this interpreter can only run inside isolated Restore,
// after an imported explicit validated local NN and its immediately paired SET.
func (e *Engine) restoreNoInheritNotNull(op *Operation, raw *parser.Update, origin restoredNotNull) error {
	name, column, label, err := restoreNoInheritSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_constraint")
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore no-inherit authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || old.relationKind != tableRelation || origin.ref != ref {
		e.mu.Unlock()
		return sqlError("0A000", restoreNoInheritBoundary)
	}
	slot, ok := old.index[column]
	if !ok || old.attributes[slot] != origin.attribute || old.columns[slot].Nullable {
		e.mu.Unlock()
		return sqlError("0A000", restoreNoInheritBoundary)
	}
	index := slices.IndexFunc(old.notNullConstraints, func(c notNullConstraint) bool {
		return c.OID == origin.constraint && c.Name == label && c.Attribute == origin.attribute && !c.NotValid
	})
	if index < 0 {
		e.mu.Unlock()
		return sqlError("0A000", restoreNoInheritBoundary)
	}
	for _, constraint := range old.uniqueConstraints {
		if constraint.Primary && slices.Contains(constraint.Attributes, origin.attribute) {
			e.mu.Unlock()
			return sqlError("0A000", restoreNoInheritBoundary)
		}
	}
	if graph := old.inheritance; graph != nil {
		fact := graph.notNull[origin.attribute]
		if !fact.local || fact.count() != 0 {
			e.mu.Unlock()
			return sqlError("0A000", restoreNoInheritBoundary)
		}
	}
	if old.notNullConstraints[index].NoInherit {
		e.mu.Unlock()
		return nil
	}
	if old.revision == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.revision++
	next.notNullConstraints = slices.Clone(old.notNullConstraints)
	next.notNullConstraints[index].NoInherit = true
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch})
	return err
}

func restoreNoInheritSyntax(raw *parser.Update) (parser.ObjectName, string, string, error) {
	reject := func() (parser.ObjectName, string, string, error) {
		return parser.ObjectName{}, "", "", sqlError("0A000", restoreNoInheritBoundary)
	}
	if !restoreCatalogName(raw.Name, "pg_constraint") || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) != 1 {
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
	if !ok || from.Only || !restoreCatalogName(from.Name, "pg_attribute") {
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

func restoreCatalogName(name parser.ObjectName, relation string) bool {
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
