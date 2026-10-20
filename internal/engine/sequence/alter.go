package sequence

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// IdentityOwnershipError rejects OWNED BY on identity sequence d of table.
func IdentityOwnershipError(d, table *catalog.Relation) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot change ownership of identity sequence", Detail: fmt.Sprintf(`Sequence "%s" is linked to table "%s".`, d.Name(), table.Name())}
}

// TemporaryLoggedError rejects SET LOGGED or UNLOGGED of TEMP sequence d in
// schema.
func TemporaryLoggedError(d *catalog.Relation, schema string) error {
	diagnostic := sqlError("42P16", fmt.Sprintf(`cannot change logged status of table "%s" because it is temporary`, d.Name())).(*sqlstate.Error)
	diagnostic.Schema, diagnostic.Table = schema, d.Name()
	return diagnostic
}

// CheckSchemaMove rejects SET SCHEMA into or out of a TEMP schema.
func CheckSchemaMove(from, to *catalog.Namespace) error {
	if from.Temporary != (ids.SessionToken{}) || to.Temporary != (ids.SessionToken{}) {
		return sqlError("0A000", "cannot move objects into or out of temporary schemas")
	}
	return nil
}

// NotSequenceError rejects DROP SEQUENCE of relation d that is not a
// sequence, hinting at the matching DROP.
func NotSequenceError(d *catalog.Relation) error {
	hint := "Use DROP TABLE to remove a table."
	if d.RelationKind == catalog.IndexRelation {
		hint = "Use DROP INDEX to remove an index."
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a sequence`, d.Name()), Hint: hint}
}

// Effects is what an ALTER SEQUENCE publishes beside the new descriptor.
type Effects struct {
	// PlanStamp bumps the relation plan stamp, invalidating prepared literals.
	PlanStamp bool
	// ReplaceGenerator publishes the generator in a new generation.
	ReplaceGenerator bool
	// DiscardTail drops the caller's unused cache tail.
	DiscardTail bool
}

// AlterEffects decides the effects of ALTER SEQUENCE from old to next with
// options raw and action. Any option other than OWNED BY, or a name,
// schema, owner or persistence change, bumps the plan stamp; OWNED BY alone
// and an unchanged no-op do not.
func AlterEffects(old, next *catalog.Relation, raw []parser.SequenceOption, action string) Effects {
	rewrite := false
	for _, option := range raw {
		rewrite = rewrite || option.Name != "owned"
	}
	persistenceChanged := next.Sequence.Persistence != old.Sequence.Persistence
	return Effects{
		PlanStamp:        rewrite || next.Name() != old.Name() || next.Namespace() != old.Namespace() || next.Owner != old.Owner || persistenceChanged,
		ReplaceGenerator: rewrite || persistenceChanged,
		DiscardTail:      action == "" || persistenceChanged,
	}
}
