package catalog

import "stuntgres/internal/engine/ids"

// RulesPayload is a relation's ordered rewrite rules, as
// Relation.Rules holds them. Catalog code validates them and reads their
// facts; the concrete stored.Rule, with its condition and action bindings
// (cuts 8 and 9), sits above catalog behind stored.RulesOf.
type RulesPayload interface {
	// Validate checks the rules against the relation that owns them.
	Validate(v Read, owner *Relation) error
	// Len returns the number of rules.
	Len() int
	// Visit calls visit for each rule in stored order and stops at the first
	// error, which it returns.
	Visit(visit func(RuleFact) error) error
	// RoutineRecipe returns the original presentation of an action slot;
	// action -1 selects the condition. Missing rules or slots return nil.
	RoutineRecipe(rule ids.OID, action int) ViewPayload
}

// RuleFact is the catalog-visible identity of one rewrite rule: its
// pg_rewrite OID, name, ev_type code, INSTEAD flag and the objects its
// condition and actions read. References is shared with the payload and must
// not be modified.
type RuleFact struct {
	OID         ids.OID
	Name, Event string
	Instead     bool
	References  []ids.ObjectAddress
}

// RuleRoutineRecipe identifies a genuine stored rule occurrence grafted into
// a command. The live rule slot must still own this exact presentation before
// its original manifest can be borrowed; this token grants no permissions.
type RuleRoutineRecipe struct {
	Owner        *Relation
	Rule         ids.OID
	Action       int
	Presentation ViewPayload
}

func (r *RuleRoutineRecipe) RoutineRecipeOID() uint32 { return uint32(r.Rule) }

// HasRules reports whether d carries at least one rewrite rule.
func (d *Relation) HasRules() bool {
	return d.Rules != nil && d.Rules.Len() != 0
}

// VisitRules visits d's rules in stored order; a relation without a rules
// payload visits nothing.
func (d *Relation) VisitRules(visit func(RuleFact) error) error {
	if d.Rules == nil {
		return nil
	}
	return d.Rules.Visit(visit)
}
