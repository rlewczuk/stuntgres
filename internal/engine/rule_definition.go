package engine

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func (e *Engine) bindRuleDefinitionLocked(scope *StatementScope, auth AuthorizationView, d *catalog.Relation, ref ids.ObjectRef, s *parser.CreateRule) (stored.Rule, error) {
	p := stored.RuleParts{Name: s.Name.Text, Event: strings.ToUpper(s.Event), Instead: s.Instead}
	ns := &analyze.RuleNamespace{Relation: d, Ref: ref, Event: p.Event}
	if s.Condition != nil {
		binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
		binder.Target.RuleNamespace = ns
		binder.Params.NoParameters = true
		binder.Query.Auth = &auth
		binder.Clause.AggregateClause = "WHERE"
		binder.Query.Frame = 1
		binder.Query.NextQueryFrame = 1
		var err error
		p.Condition, err = binder.Bind(s.Condition, analyze.ExpressionPolicy{Context: analyze.FilterContext})
		if err != nil {
			return stored.NewRule(p), err
		}
		normalizedCondition := binder.NormalizeStoredExpression(s.Condition)
		if err := binder.CaptureViewLiterals(normalizedCondition, p.Condition, analyze.ViewTargetLiteral, 0); err != nil {
			return stored.NewRule(p), err
		}
		query := &parser.Select{Targets: []parser.Expr{normalizedCondition}}
		b := analyze.Binding{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "condition", Usage: p.Condition.ResultUsage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{p.Condition}, Frame: binder.FrameID(), Syntax: query}}, Dependencies: binder.Dependencies, QuerySources: binder.Query.Sources, Capture: analyze.ViewCapture{Columns: binder.Capture.Columns, Literals: binder.Capture.Literals, CastUsages: binder.Capture.CastUsages, Composites: binder.Capture.Composites, Collations: binder.Capture.Collations, Namespaces: binder.Capture.Namespaces}, Prepared: analyze.PreparedCaptures{SequenceConstants: binder.Prepared.SequenceConstants, RegtypeConstants: binder.Prepared.RegtypeConstants}}
		b.Capture.RoutineRefs = binder.Capture.RoutineRefs
		b.Capture.OperatorRefs = binder.Capture.OperatorRefs
		for i := range b.QuerySources {
			b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
		}
		p.ConditionBinding = &b
		p.ConditionPresentation, err = stored.FreezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), query, b, false, false, 0, "", false)
		if err != nil {
			return stored.NewRule(p), err
		}
	}
	providers := 0
	for _, action := range s.Actions {
		if _, notify := action.(*parser.Notify); notify {
			return stored.NewRule(p), sqlError("0A000", "NOTIFY is unsupported")
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
			return stored.NewRule(p), ddl.CreationError(err, targetName)
		}
		b, err := describeWithRule(scope.op.Context(), e.analysisHost(scope.op), action, auth, target, scope.input, nil, nil, ns)
		if err != nil {
			return stored.NewRule(p), err
		}
		if p.Condition != nil && view.RuleSetAction(b, action) {
			return stored.NewRule(p), sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
		}
		if err := view.ValidateRuleActionCTEs(action, b); err != nil {
			return stored.NewRule(p), err
		}
		b = view.RetainRuleCTEPresentation(auth.Catalog, b)
		b.Rule.Template = true
		if b.Parameters.Count != 0 {
			return stored.NewRule(p), sqlError("42P02", "there is no parameter $1")
		}
		for i := range b.QuerySources {
			b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
		}
		if b.Returning != nil {
			if p.Condition != nil {
				return stored.NewRule(p), sqlError("0A000", "RETURNING lists are not supported in conditional rules")
			}
			if !p.Instead {
				return stored.NewRule(p), sqlError("0A000", "RETURNING lists are not supported in non-INSTEAD rules")
			}
			providers++
			if providers > 1 {
				return stored.NewRule(p), sqlError("0A000", "cannot have multiple RETURNING lists in a rule")
			}
			if err := view.RuleResultCheck(d, b.Returning.Schema, false); err != nil {
				return stored.NewRule(p), err
			}
		}
		presentation, err := e.freezeRuleActionPresentationLocked(auth, action, b)
		if err != nil {
			return stored.NewRule(p), err
		}
		modifiers, err := e.freezeRuleModifierPresentationsLocked(auth, b)
		if err != nil {
			return stored.NewRule(p), err
		}
		p.Actions = append(p.Actions, stored.NewRuleAction(stored.RuleActionParts{Syntax: action, Binding: b, Presentation: presentation, ModifierPresentations: modifiers}))
	}
	var err error
	p.References, err = view.RuleReferences(scope.op.Context(), auth.Catalog, stored.NewRule(p))
	return stored.NewRule(p), err
}
