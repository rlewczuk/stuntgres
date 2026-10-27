package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func RuleModeAction(action parser.AlterTableActionKind) bool {
	return action == parser.EnableRule || action == parser.DisableRule || action == parser.EnableReplicaRule || action == parser.EnableAlwaysRule
}

func RuleModesOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if !RuleModeAction(action.Action) {
			return false
		}
	}
	return true
}

// RuleModeCandidate takes no locks and never changes borrowed descriptor/payload
// backing. The enclosing ALTER owns authorization, revision and publication.
func RuleModeCandidate(old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	mode := catalog.RuleOrigin
	switch action.Action {
	case parser.EnableRule:
	case parser.DisableRule:
		mode = catalog.RuleDisabled
	case parser.EnableReplicaRule:
		mode = catalog.RuleReplica
	case parser.EnableAlwaysRule:
		mode = catalog.RuleAlways
	default:
		return nil, fmt.Errorf("stuntgres: invalid rule mode action")
	}
	rules := stored.RulesOf(old)
	for i, rule := range rules {
		if rule.Name != action.Rule.Text {
			continue
		}
		if rule.Mode() == mode {
			return old, nil
		}
		next := *old
		rules = slices.Clone(rules)
		rules[i] = rule.With(func(p *stored.RuleParts) { p.Mode = mode })
		next.Rules = stored.RulesPayload(rules)
		return &next, nil
	}
	return nil, sqlError("42704", fmt.Sprintf(`rule "%s" for relation "%s" does not exist`, action.Rule.Text, old.Name()))
}

// RestoreRuleMode admits only the qualified, unconditional nondefault form
// emitted after a rule declaration. Normal ALTER still checks object authority.
func RestoreRuleMode(s *parser.AlterTable) bool {
	return !s.IfExists && !s.Only && len(s.Name.Parts) == 2 && len(s.Actions) == 1 && s.Actions[0].Action != parser.EnableRule && RuleModeAction(s.Actions[0].Action)
}
