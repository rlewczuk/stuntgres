package dump

import "strings"

// Statement is one dump statement and the role that must own its objects.
type Statement struct{ Owner, SQL string }

// Group is a block of statements for one table, emitted as its owner.
type Group struct{ Owner, Table, SQL string }

// CommentLiteral quotes comment text as an explicit escape string, which is
// independent of the destination string setting.
func CommentLiteral(text string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "''") + "'"
}
