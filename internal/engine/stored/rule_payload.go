package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RuleList is the catalog.RulesPayload of a relation. It keeps the slice
// value semantics the rules always had, so copy-on-write sharing is
// unchanged.
type RuleList []Rule

func (r RuleList) Validate(v catalog.Read, owner *catalog.Relation) error {
	return validateRuleList(v, owner, r)
}

func (r RuleList) Len() int { return len(r) }

func (r RuleList) OID(i int) ids.OID { return r[i].OID }

func (r RuleList) Visit(visit func(catalog.RuleFact) error) error {
	for _, rule := range r {
		if err := visit(catalog.RuleFact{OID: rule.OID, Name: rule.Name, Event: ruleEventCode(rule.event), Instead: rule.instead, Mode: rule.mode, References: rule.references}); err != nil {
			return err
		}
	}
	return nil
}

func (r RuleList) RoutineRecipe(oid ids.OID, action int) catalog.ViewPayload {
	for _, rule := range r {
		if rule.OID != oid {
			continue
		}
		if action == -1 {
			return ViewPayload(rule.conditionPresentation)
		}
		if action >= 0 && action < len(rule.actions) {
			return ViewPayload(rule.actions[action].presentation)
		}
		return nil
	}
	return nil
}

// RulesOf returns d's rules, or nil when d stores none.
func RulesOf(d *catalog.Relation) []Rule {
	r, _ := d.Rules.(RuleList)
	return r
}

// RulesPayload stores r as a payload. A nil slice stays a nil payload; an
// empty non-nil slice stays an empty list.
func RulesPayload(r []Rule) catalog.RulesPayload {
	if r == nil {
		return nil
	}
	return RuleList(r)
}
