package expr

import "stuntgres/internal/sqltype"

// StatementFields is a streaming, value-only projection of released selected
// statement-identity fields. It grants no evaluation, catalog or runtime access.
// Ordinary callable identities and lexical children stay with their owners.
type StatementFields interface {
	JumbleUint64(uint64)
	JumbleText(string)
	JumbleBool(bool)
	// JumbleConst projects a released implicit typed constant, without inventing
	// a runtime expression, datum, descriptor or source replacement location.
	JumbleConst(sqltype.ID)
}

// StatementJumbler projects one concrete expression's static discriminator first,
// then its selected scalar fields. Every admitted expression owns its projection;
// embedding metadata or another expression does not select the wrapper's tag.
type StatementJumbler interface{ JumbleFields(StatementFields) }

// StatementJumbleChildren overrides lexical children only where the released
// jumble deliberately ignores some analyzed operands (notably TableFunc).
type StatementJumbleChildren interface {
	JumbleChildren(func(Expr), StatementFields)
}
