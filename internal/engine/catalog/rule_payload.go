package catalog

// RulesPayload is a relation's ordered rewrite rules, as
// Relation.Rules holds them. Catalog code only validates them; the
// concrete ruleDefinition, with its condition and action bindings (cuts 8
// and 9), stays in the engine behind rulesOf.
type RulesPayload interface {
	// Validate checks the rules against the relation that owns them.
	Validate(v Read, owner *Relation) error
}
