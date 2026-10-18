package engine

import "stuntgres/internal/engine/catalog"

// ruleList is the engine's rulesPayload. It keeps the slice value semantics
// the rules always had, so copy-on-write sharing is unchanged.
type ruleList []ruleDefinition

func (r ruleList) Validate(v catalog.Read, owner *catalog.Relation) error {
	return validateRuleList(v, owner, r)
}

// rulesOf returns d's rules, or nil when d stores none.
func rulesOf(d *catalog.Relation) []ruleDefinition {
	r, _ := d.Rules.(ruleList)
	return r
}

// rulesPayloadOf stores r as a payload. A nil slice stays a nil payload; an
// empty non-nil slice stays an empty list.
func rulesPayloadOf(r []ruleDefinition) catalog.RulesPayload {
	if r == nil {
		return nil
	}
	return ruleList(r)
}
