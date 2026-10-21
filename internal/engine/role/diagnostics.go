// Package role decides role and ACL DDL: GRANT/REVOKE, ALTER DEFAULT
// PRIVILEGES, ALTER ... OWNER and REASSIGN/DROP OWNED plans and staging, and
// the role diagnostics. The engine captures authority and publishes.
package role

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// EmitPasswordNotice emits the NOTICE or WARNING a password option raises.
func EmitPasswordNotice(op *session.Operation, n authorization.PasswordNotice) {
	switch n {
	case authorization.PasswordEmptyNotice:
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"})
	case authorization.PasswordMD5Notice:
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."})
	}
}

// Diagnostic is a role DDL ERROR with a DETAIL line (none when detail is
// empty).
func Diagnostic(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}

// ConnectionLimitError is the FATAL error of a role at its connection limit.
func ConnectionLimitError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: "too many connections for role \"" + name + "\""}
}

// LoginError is the FATAL error of a role without LOGIN.
func LoginError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: "role \"" + name + "\" is not permitted to log in"}
}
