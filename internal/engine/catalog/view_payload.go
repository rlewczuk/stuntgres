package catalog

import (
	"stuntgres/internal/engine/ids"
)

// ViewPayload is the frozen stored-SQL presentation of a view or
// materialized view, as Relation.View holds it. It exposes only
// what catalog code needs: the rewrite rule identity, dependencies, temporary
// ownership, options and validation. The concrete *stored.View, which
// holds plan IR, sits above catalog behind stored.ViewOf.
type ViewPayload interface {
	// RuleOID returns the pg_rewrite identity of the view's _RETURN rule.
	RuleOID() ids.OID
	// DefinitionRevision identifies the frozen defining expression expansion.
	DefinitionRevision() ids.DefinitionRevision
	// Dependencies returns the frozen expression dependencies, shared with
	// the payload; callers must not modify the slice.
	Dependencies() []ExpressionDependency
	// DependsOn reports whether the presentation reads target. A zero SubID
	// matches the whole object.
	DependsOn(target ids.ObjectAddress) bool
	// TemporaryConsumer returns the relation that owns the presentation's
	// temporary captures and the session that owns them; both are zero for a
	// presentation without temporary references.
	TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken)
	// CapturedReference returns the descriptor the presentation captured for
	// a temporary reference.
	CapturedReference(ref ids.ObjectRef) (Descriptor, bool)
	// Validate checks the presentation against its owning relation.
	Validate(v Read, owner *Relation) error
	// Options returns the view's reloptions as "name=value" strings, shared
	// with the payload; callers must not modify the slice.
	Options() []string
}
