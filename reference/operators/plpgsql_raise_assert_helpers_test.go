package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func raiseAssertDO(body string) string { return "DO $$BEGIN " + body + " END$$" }

func raiseAssertError(t *testing.T, p *fixture.Pair, c *sql.Conn, body, code, message, action string) {
	t.Helper()
	plpgsqlError(t, p, c, raiseAssertDO(body), false,
		plpgsqlRuntimeDiagnostic(code, message, "inline_code_block", action))
}

func raiseAssertNotice(level, code, message, function string) fixture.Diagnostic {
	return fixture.Diagnostic{
		Severity: level, SeverityUnlocalized: level, Code: code, Message: message,
		Context: "PL/pgSQL function " + function + " line 1 at RAISE",
	}
}

func raiseAssertNotices(t *testing.T, got, want []fixture.Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ordered notices got=%#v want=%#v", got, want)
	}
}
