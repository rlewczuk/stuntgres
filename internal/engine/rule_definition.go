package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Immutable templates use the same bound mutations and query graph as ordinary
// commands. Pseudo columns are analysis-only lexical frames, never row callbacks.
const rulePseudoScope = ^uint32(0)

type ruleNamespace struct {
	relation *catalog.Relation
	ref      ids.ObjectRef
	event    string
}

type ruleAction struct {
	syntax                parser.Statement
	binding               binding
	presentation          *viewDefinition
	modifierPresentations map[parser.Statement]*viewDefinition
}

type ruleDefinition struct {
	OID                   ids.OID
	Name                  string
	event                 string
	instead               bool
	condition             expr.Expr
	conditionPresentation *viewDefinition
	conditionBinding      *binding
	actions               []ruleAction
	references            []ids.ObjectAddress
}

func ruleIndex(d *catalog.Relation, name string) int {
	return slices.IndexFunc(rulesOf(d), func(r ruleDefinition) bool { return r.Name == name })
}
func ruleMissing(name string, d *catalog.Relation) error {
	return sqlError("42704", fmt.Sprintf(`rule "%s" for relation "%s" does not exist`, name, d.Name()))
}
func ruleDuplicate(name string, d *catalog.Relation) error {
	return sqlError("42710", fmt.Sprintf(`rule "%s" for relation "%s" already exists`, name, d.Name()))
}
func ruleEvent(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.Insert:
		return "INSERT"
	case *parser.Update:
		return "UPDATE"
	case *parser.Delete:
		return "DELETE"
	case *parser.Select:
		return "SELECT"
	}
	return ""
}
func ruleEventCode(event string) string {
	switch event {
	case "SELECT":
		return "1"
	case "UPDATE":
		return "2"
	case "INSERT":
		return "3"
	case "DELETE":
		return "4"
	}
	return ""
}

func (b *expressionBinder) ruleColumn(raw parser.ColumnExpr) (expr.Expr, bool, error) {
	if b.target.ruleNamespace == nil || len(raw.Name.Parts) == 0 {
		return nil, false, nil
	}
	side := raw.Name.Parts[0].Text
	if side != "new" && side != "old" {
		return nil, false, nil
	}
	ns := b.target.ruleNamespace
	if side == "old" && ns.event == "INSERT" || side == "new" && ns.event == "DELETE" || ns.event == "SELECT" {
		return nil, true, sqlError("42P17", fmt.Sprintf("ON %s rule cannot use %s", ns.event, strings.ToUpper(side)))
	}
	if len(raw.Name.Parts) == 1 {
		row, err := b.ruleWholeRow(raw.Name.Parts[0], raw.Pos)
		return row, true, err
	}
	if len(raw.Name.Parts) > 2 {
		first := raw
		first.Name.Parts = raw.Name.Parts[:2]
		base, _, err := b.ruleColumn(first)
		if err != nil {
			return nil, true, err
		}
		for _, field := range raw.Name.Parts[2:] {
			base, err = b.selectCompositeField(base, field.Text, field.Pos)
			if err != nil {
				return nil, true, err
			}
		}
		return base, true, nil
	}
	name := raw.Name.Parts[1].Text
	slot, ok := ns.relation.Index[name]
	if !ok {
		return nil, true, expr.ErrorAt("42703", fmt.Sprintf(`column %s.%s does not exist`, side, name), raw.Pos)
	}
	ref := ns.ref
	ref.Address.SubID = ns.relation.Attributes[slot]
	frame := uint32(1)
	if side == "new" {
		frame = 2
	}
	b.dependencies = append(b.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: ns.relation.Revision()})
	return columnExpression{Info: b.info(ns.relation.Columns[slot].Usage, raw.Pos, expr.Properties{RowDependent: true}), origin: ref, scope: rulePseudoScope, frame: frame, slot: slot}, true, nil
}

func ruleResultCheck(d *catalog.Relation, schema []catalog.Column, selectRule bool) error {
	prefix := "RETURNING list"
	if selectRule {
		prefix = "SELECT rule's target list"
	}
	if d.ColumnMeta.HighWater > int32(len(d.Columns)) {
		message := "cannot create a RETURNING list for a relation containing dropped columns"
		if selectRule {
			message = "cannot convert relation containing dropped columns to view"
		}
		return sqlError("0A000", message)
	}
	if len(schema) < len(d.Columns) {
		return sqlError("42P17", prefix+" has too few entries")
	}
	if len(schema) > len(d.Columns) {
		return sqlError("42P17", prefix+" has too many entries")
	}
	for i, column := range schema {
		want := d.Columns[i]
		if selectRule && column.Name != want.Name {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`SELECT rule's target entry %d has different column name from column "%s"`, i+1, want.Name), Detail: fmt.Sprintf(`SELECT target entry is named "%s".`, column.Name)}
		}
		if column.Usage.Type != want.Usage.Type {
			prefix = "RETURNING list's"
			if selectRule {
				prefix = "SELECT rule's target"
			}
			detail := "RETURNING list entry"
			if selectRule {
				detail = "SELECT target entry"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`%s entry %d has different type from column "%s"`, prefix, i+1, want.Name), Detail: fmt.Sprintf("%s has type %s, but column has type %s.", detail, column.Usage.Type.DiagnosticName(), want.Usage.Type.DiagnosticName())}
		}
		if want.Usage.Typmod != -1 && column.Usage.Typmod != -1 && column.Usage.Typmod != want.Usage.Typmod {
			prefix = "RETURNING list's"
			detail := "RETURNING list entry"
			if selectRule {
				prefix = "SELECT rule's target"
				detail = "SELECT target entry"
			}
			actual, err := pgType(column.Usage)
			if err != nil {
				return err
			}
			expected, err := pgType(want.Usage)
			if err != nil {
				return err
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`%s entry %d has different size from column "%s"`, prefix, i+1, want.Name), Detail: fmt.Sprintf("%s has type %s, but column has type %s.", detail, actual, expected)}
		}
	}
	return nil
}

func ruleReturning(s parser.Statement) *parser.Returning {
	switch s := s.(type) {
	case *parser.Insert:
		return s.Returning
	case *parser.Update:
		return s.Returning
	case *parser.Delete:
		return s.Returning
	}
	return nil
}

func (e *Engine) bindRuleDefinitionLocked(scope *StatementScope, auth AuthorizationView, d *catalog.Relation, ref ids.ObjectRef, s *parser.CreateRule) (ruleDefinition, error) {
	r := ruleDefinition{Name: s.Name.Text, event: strings.ToUpper(s.Event), instead: s.Instead}
	ns := &ruleNamespace{relation: d, ref: ref, event: r.event}
	if s.Condition != nil {
		binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
		binder.target.ruleNamespace = ns
		binder.params.noParameters = true
		binder.queryAuth = &auth
		binder.clause.aggregateClause = "WHERE"
		binder.queryFrame = 1
		binder.nextQueryFrame = 1
		var err error
		r.condition, err = binder.bind(s.Condition, expressionPolicy{context: filterContext})
		if err != nil {
			return r, err
		}
		normalizedCondition := binder.normalizeStoredExpression(s.Condition)
		if err := binder.captureViewLiterals(normalizedCondition, r.condition, viewTargetLiteral, 0); err != nil {
			return r, err
		}
		query := &parser.Select{Targets: []parser.Expr{normalizedCondition}}
		b := binding{query: &BoundQuery{Root: &projectNode{querySchema: querySchema{{Name: "condition", Usage: r.condition.ResultUsage()}}, child: &singletonNode{}, expressions: []expr.Expr{r.condition}, frame: binder.frameID(), syntax: query}}, dependencies: binder.dependencies, querySources: binder.querySources, capture: viewCapture{columns: binder.capture.columns, literals: binder.capture.literals, castUsages: binder.capture.castUsages, composites: binder.capture.composites, collations: binder.capture.collations, namespaces: binder.capture.namespaces}, prepared: preparedCaptures{sequenceConstants: binder.prepared.sequenceConstants, regtypeConstants: binder.prepared.regtypeConstants}}
		b.capture.routineRefs = binder.capture.routineRefs
		b.capture.operatorRefs = binder.capture.operatorRefs
		for i := range b.querySources {
			b.querySources[i].frozenAttributes = slices.Clone(b.querySources[i].relation.Attributes)
		}
		r.conditionBinding = &b
		r.conditionPresentation, err = e.freezeQueryPresentationLocked(auth, query, b, false, false, 0, "", false)
		if err != nil {
			return r, err
		}
	}
	providers := 0
	for _, action := range s.Actions {
		if _, notify := action.(*parser.Notify); notify {
			return r, sqlError("0A000", "NOTIFY is unsupported")
		}
		var target ids.ObjectRef
		var err error
		var targetName parser.ObjectName
		switch action := action.(type) {
		case *parser.Insert:
			targetName = action.Name
		case *parser.Update:
			targetName = action.Name
		case *parser.Delete:
			targetName = action.Name
		}
		if len(targetName.Parts) != 0 {
			target, err = e.resolveRelationReference(auth, targetName, true)
		}
		if err != nil {
			return r, creationError(err, targetName)
		}
		b, err := e.describeWithRule(scope.op.Context(), action, auth, target, scope.input, scope.op, nil, nil, ns)
		if err != nil {
			return r, err
		}
		if r.condition != nil && ruleSetAction(b, action) {
			return r, sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
		}
		if err := validateRuleActionCTEs(action, b); err != nil {
			return r, err
		}
		b = retainRuleCTEPresentation(auth.Catalog, b)
		b.rule.template = true
		if b.parameters.Count != 0 {
			return r, sqlError("42P02", "there is no parameter $1")
		}
		for i := range b.querySources {
			b.querySources[i].frozenAttributes = slices.Clone(b.querySources[i].relation.Attributes)
		}
		if b.returning != nil {
			if r.condition != nil {
				return r, sqlError("0A000", "RETURNING lists are not supported in conditional rules")
			}
			if !r.instead {
				return r, sqlError("0A000", "RETURNING lists are not supported in non-INSTEAD rules")
			}
			providers++
			if providers > 1 {
				return r, sqlError("0A000", "cannot have multiple RETURNING lists in a rule")
			}
			if err := ruleResultCheck(d, b.returning.schema, false); err != nil {
				return r, err
			}
		}
		presentation, err := e.freezeRuleActionPresentationLocked(auth, action, b)
		if err != nil {
			return r, err
		}
		modifiers, err := e.freezeRuleModifierPresentationsLocked(auth, b)
		if err != nil {
			return r, err
		}
		r.actions = append(r.actions, ruleAction{syntax: action, binding: b, presentation: presentation, modifierPresentations: modifiers})
	}
	var err error
	r.references, err = ruleReferences(scope.op.Context(), auth.Catalog, r)
	return r, err
}
