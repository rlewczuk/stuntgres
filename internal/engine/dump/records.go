package dump

import (
	"strings"

	"stuntgres/internal/engine/ids"
)

// Statement is one dump statement and the role that must own its objects.
type Statement struct {
	Owner, SQL string
	// FollowingSQL contains owner/comment metadata outside the definition window.
	FollowingSQL string
	Binding      *DefinitionBinding
}

// DefinitionBinding is an engine-owned requirement of one actual DDL, not a
// caller-supplied search path or general setting directive.
type DefinitionBinding struct{ CubeNamespace ids.ObjectAddress }

// Group is a block of statements for one table, emitted as its owner.
type Group struct {
	Owner, Table, SQL string
	Binding           *DefinitionBinding
}

// CommentLiteral quotes comment text as an explicit escape string, which is
// independent of the destination string setting.
func CommentLiteral(text string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "''") + "'"
}
