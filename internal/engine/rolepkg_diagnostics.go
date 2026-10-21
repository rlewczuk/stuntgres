package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func emitPasswordNotice(op *session.Operation, n authorization.PasswordNotice) {
	switch n {
	case authorization.PasswordEmptyNotice:
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"})
	case authorization.PasswordMD5Notice:
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."})
	}
}

func roleDiagnostic(code, message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}

func roleConnectionLimitError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: "too many connections for role \"" + name + "\""}
}

func roleLoginError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: "role \"" + name + "\" is not permitted to log in"}
}
