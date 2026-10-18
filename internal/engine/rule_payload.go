package engine

// rulesPayload is a relation's ordered rewrite rules, as
// relationDescriptor.Rules holds them. Catalog code only validates them; the
// concrete ruleDefinition, with its condition and action bindings (cuts 8
// and 9), stays in the engine behind rulesOf.
type rulesPayload interface {
	// Validate checks the rules against the relation that owns them.
	Validate(v CatalogRead, owner *relationDescriptor) error
}

// ruleList is the engine's rulesPayload. It keeps the slice value semantics
// the rules always had, so copy-on-write sharing is unchanged.
type ruleList []ruleDefinition

func (r ruleList) Validate(v CatalogRead, owner *relationDescriptor) error {
	return validateRuleList(v, owner, r)
}

// rulesOf returns d's rules, or nil when d stores none.
func rulesOf(d *relationDescriptor) []ruleDefinition {
	r, _ := d.Rules.(ruleList)
	return r
}

// rulesPayloadOf stores r as a payload. A nil slice stays a nil payload; an
// empty non-nil slice stays an empty list.
func rulesPayloadOf(r []ruleDefinition) rulesPayload {
	if r == nil {
		return nil
	}
	return ruleList(r)
}
