package view

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindCondition binds and freezes the qualification once, before any action.
// The returned capture is also the engine's authority for dependency holds.
func (p *RulePlan) BindCondition(h Host, auth ddl.Authority, input sqltype.InputEnvironment) (analyze.Binding, error) {
	s, create := p.stmt.(*parser.CreateRule)
	if !create || p.parts.Event == "" || s.Condition == nil {
		return analyze.Binding{}, nil
	}
	ns := &analyze.RuleNamespace{Relation: p.old, Ref: p.ref, Event: p.parts.Event}
	condition, query, b, err := h.BindRuleCondition(auth, ns, s.Condition, input)
	if err != nil {
		return b, err
	}
	presentation, err := stored.FreezeQueryPresentation(h.AccessView(auth).Catalog, h.TemporaryOwner(auth), query, b, false, false, 0, "", false)
	if err != nil {
		return b, err
	}
	p.parts.Condition, p.parts.ConditionBinding, p.parts.ConditionPresentation = condition, &b, presentation
	return b, nil
}

// BindAction completes one admitted action in syntax order. All restrictions,
// recipe slots and presentations belong to this single canonical binding.
func (p *RulePlan) BindAction(h Host, auth ddl.Authority, ordinal int, input sqltype.InputEnvironment) (analyze.Binding, error) {
	if p.parts.Event == "" {
		return p.bindSelectAction(h, auth, input)
	}
	catalogView, owner := h.AccessView(auth).Catalog, h.TemporaryOwner(auth)
	s := p.stmt.(*parser.CreateRule)
	action := s.Actions[ordinal]
	if _, notify := action.(*parser.Notify); notify {
		return analyze.Binding{}, sqlError("0A000", "NOTIFY is unsupported")
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
		target, err = h.ResolveRelation(auth, targetName)
	}
	if err != nil {
		return analyze.Binding{}, ddl.CreationError(err, targetName)
	}
	ns := &analyze.RuleNamespace{Relation: p.old, Ref: p.ref, Event: p.parts.Event}
	b, err := h.DescribeRule(auth, action, target, input, ns)
	if err != nil {
		return b, err
	}
	if p.parts.Condition != nil && RuleSetAction(b, action) {
		return b, sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
	}
	if err := ValidateRuleActionCTEs(action, b); err != nil {
		return b, err
	}
	b = RetainRuleCTEPresentation(catalogView, b)
	b.Rule.Template = true
	if b.Parameters.Count != 0 {
		return b, sqlError("42P02", "there is no parameter $1")
	}
	for i := range b.QuerySources {
		b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
	}
	if b.Returning != nil {
		if p.parts.Condition != nil {
			return b, sqlError("0A000", "RETURNING lists are not supported in conditional rules")
		}
		if !p.parts.Instead {
			return b, sqlError("0A000", "RETURNING lists are not supported in non-INSTEAD rules")
		}
		p.providers++
		if p.providers > 1 {
			return b, sqlError("0A000", "cannot have multiple RETURNING lists in a rule")
		}
		if err := RuleResultCheck(p.old, b.Returning.Schema, false); err != nil {
			return b, err
		}
	}
	presentation, err := freezeRuleActionPresentation(catalogView, owner, action, b)
	if err != nil {
		return b, err
	}
	modifiers, err := freezeRuleModifierPresentations(catalogView, owner, b)
	if err != nil {
		return b, err
	}
	p.parts.Actions = append(p.parts.Actions, stored.NewRuleAction(stored.RuleActionParts{Syntax: action, Binding: b, Presentation: presentation, ModifierPresentations: modifiers}))
	return b, nil
}
