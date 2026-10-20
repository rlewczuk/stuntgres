package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Immutable templates use the same bound mutations and query graph as ordinary
// commands. Pseudo columns are analysis-only lexical frames, never row callbacks.
const RulePseudoScope = ^uint32(0)

// RuleNamespace is the relation a rule is defined on, with the rule event; it
// supplies the rule's OLD and NEW pseudo columns.
type RuleNamespace struct {
	Relation *catalog.Relation
	Ref      ids.ObjectRef
	Event    string
}
