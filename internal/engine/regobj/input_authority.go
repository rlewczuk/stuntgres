package regobj

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Authority is the read authority a captured input resolver checks
// names with: the statement's catalog and roles, its environment and
// the sparse definition overlay of a cooking DDL statement.
type Authority struct {
	View        access.View
	Environment access.Environment
	Definitions catalog.DefinitionReader // nil outside cooking DDL
}

// InputCapture is what a captured input resolver keeps from its statement:
// its authority, its lookup path, and the session's database name, token
// and TEMP namespace as they were at capture.
type InputCapture struct {
	Authority Authority
	Path      []ids.ObjectAddress
	Database  string
	Token     ids.SessionToken
	Temporary ids.ObjectAddress
}

// NoticeHost is the statement capability a resolver emits type-name
// parser notices and type-modifier warnings through. The root
// statementHost implements it.
type NoticeHost interface {
	TypeNameNotice(level session.NoticeLevel, notice sqlstate.Error)
}
