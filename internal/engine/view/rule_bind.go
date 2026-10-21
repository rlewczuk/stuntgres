package view

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindRuleDefinition binds and freezes a CREATE RULE on d (ref) that is not an
// ON SELECT rule: its condition, then each action in order, with the
// unsupported forms, parameters and RETURNING rejected as PostgreSQL does.
func bindRuleDefinition(h Host, auth ddl.Authority, d *catalog.Relation, ref ids.ObjectRef, s *parser.CreateRule, input sqltype.InputEnvironment) (stored.Rule, error) {
	catalogView, owner := h.AccessView(auth).Catalog, h.TemporaryOwner(auth)
	p := stored.RuleParts{Name: s.Name.Text, Event: strings.ToUpper(s.Event), Instead: s.Instead}
	ns := &analyze.RuleNamespace{Relation: d, Ref: ref, Event: p.Event}
	if s.Condition != nil {
		condition, query, b, err := h.BindRuleCondition(auth, ns, s.Condition, input)
		p.Condition = condition
		if err != nil {
			return stored.NewRule(p), err
		}
		p.ConditionBinding = &b
		p.ConditionPresentation, err = stored.FreezeQueryPresentation(catalogView, owner, query, b, false, false, 0, "", false)
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
			target, err = h.ResolveRelation(auth, targetName)
		}
		if err != nil {
			return stored.NewRule(p), ddl.CreationError(err, targetName)
		}
		b, err := h.DescribeRule(auth, action, target, input, ns)
		if err != nil {
			return stored.NewRule(p), err
		}
		if p.Condition != nil && RuleSetAction(b, action) {
			return stored.NewRule(p), sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
		}
		if err := ValidateRuleActionCTEs(action, b); err != nil {
			return stored.NewRule(p), err
		}
		b = RetainRuleCTEPresentation(catalogView, b)
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
			if err := RuleResultCheck(d, b.Returning.Schema, false); err != nil {
				return stored.NewRule(p), err
			}
		}
		presentation, err := freezeRuleActionPresentation(catalogView, owner, action, b)
		if err != nil {
			return stored.NewRule(p), err
		}
		modifiers, err := freezeRuleModifierPresentations(catalogView, owner, b)
		if err != nil {
			return stored.NewRule(p), err
		}
		p.Actions = append(p.Actions, stored.NewRuleAction(stored.RuleActionParts{Syntax: action, Binding: b, Presentation: presentation, ModifierPresentations: modifiers}))
	}
	var err error
	p.References, err = RuleReferences(h.Context(), catalogView, stored.NewRule(p))
	return stored.NewRule(p), err
}
