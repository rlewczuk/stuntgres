package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ruleIndex(d *catalog.Relation, name string) int {
	return slices.IndexFunc(rulesOf(d), func(r ruleDefinition) bool { return r.Name == name })
}
func ruleMissing(name string, d *catalog.Relation) error {
	return sqlError("42704", fmt.Sprintf(`rule "%s" for relation "%s" does not exist`, name, d.Name()))
}
func ruleDuplicate(name string, d *catalog.Relation) error {
	return sqlError("42710", fmt.Sprintf(`rule "%s" for relation "%s" already exists`, name, d.Name()))
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
			actual, err := analyze.PGType(column.Usage)
			if err != nil {
				return err
			}
			expected, err := analyze.PGType(want.Usage)
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
	ns := &analyze.RuleNamespace{Relation: d, Ref: ref, Event: r.event}
	if s.Condition != nil {
		binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
		binder.Target.RuleNamespace = ns
		binder.Params.NoParameters = true
		binder.Query.Auth = &auth
		binder.Clause.AggregateClause = "WHERE"
		binder.Query.Frame = 1
		binder.Query.NextQueryFrame = 1
		var err error
		r.condition, err = binder.Bind(s.Condition, analyze.ExpressionPolicy{Context: analyze.FilterContext})
		if err != nil {
			return r, err
		}
		normalizedCondition := binder.NormalizeStoredExpression(s.Condition)
		if err := binder.CaptureViewLiterals(normalizedCondition, r.condition, analyze.ViewTargetLiteral, 0); err != nil {
			return r, err
		}
		query := &parser.Select{Targets: []parser.Expr{normalizedCondition}}
		b := analyze.Binding{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "condition", Usage: r.condition.ResultUsage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{r.condition}, Frame: binder.FrameID(), Syntax: query}}, Dependencies: binder.Dependencies, QuerySources: binder.Query.Sources, Capture: analyze.ViewCapture{Columns: binder.Capture.Columns, Literals: binder.Capture.Literals, CastUsages: binder.Capture.CastUsages, Composites: binder.Capture.Composites, Collations: binder.Capture.Collations, Namespaces: binder.Capture.Namespaces}, Prepared: analyze.PreparedCaptures{SequenceConstants: binder.Prepared.SequenceConstants, RegtypeConstants: binder.Prepared.RegtypeConstants}}
		b.Capture.RoutineRefs = binder.Capture.RoutineRefs
		b.Capture.OperatorRefs = binder.Capture.OperatorRefs
		for i := range b.QuerySources {
			b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
		}
		r.conditionBinding = &b
		r.conditionPresentation, err = stored.FreezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), query, b, false, false, 0, "", false)
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
		b, err := describeWithRule(scope.op.Context(), e.analysisHost(scope.op), action, auth, target, scope.input, nil, nil, ns)
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
		b.Rule.Template = true
		if b.Parameters.Count != 0 {
			return r, sqlError("42P02", "there is no parameter $1")
		}
		for i := range b.QuerySources {
			b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
		}
		if b.Returning != nil {
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
			if err := ruleResultCheck(d, b.Returning.Schema, false); err != nil {
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
