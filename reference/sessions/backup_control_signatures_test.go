package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBackupControlSignaturesDiagnostics(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		backupCatalog(t, p, b, c)
		backupDiagnosticPrecedence(t, p, b, c)
		for _, call := range []string{"pg_backup_start(NULL)", "pg_backup_start(label=>NULL)", "pg_backup_start(NULL,fast=>true)", "pg_backup_start(fast=>true,label=>NULL)", "pg_backup_start(fast:=false,label:=NULL)", "pg_catalog.PG_BACKUP_START(LABEL=>NULL)", "pg_backup_stop(wait_for_archive=>NULL)", "pg_backup_stop(wait_for_archive:=NULL)"} {
			typ := "PG_LSN"
			if strings.Contains(strings.ToLower(call), "stop") {
				typ = "RECORD"
			}
			backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT "+call+" AS v", []string{"v"}, []string{typ}, [][]any{{nil}}))
		}
		for _, tc := range []struct{ call, signature string }{
			{"pg_backup_start()", "pg_backup_start()"},
			{"pg_backup_start(1)", "pg_backup_start(integer)"},
			{"pg_backup_start('x',1)", "pg_backup_start(unknown, integer)"},
			{"pg_backup_stop(1)", "pg_backup_stop(integer)"},
			{"pg_backup_start(fast=>true)", "pg_backup_start(fast => boolean)"},
			{"pg_backup_stop(lsn=>NULL)", "pg_backup_stop(lsn => unknown)"},
			{"pg_backup_stop(labelfile=>NULL)", "pg_backup_stop(labelfile => unknown)"},
			{"pg_backup_stop(spcmapfile=>NULL)", "pg_backup_stop(spcmapfile => unknown)"},
			{"pg_backup_start('x',false,true)", "pg_backup_start(unknown, boolean, boolean)"},
			{"pg_backup_stop(false,true)", "pg_backup_stop(boolean, boolean)"},
			{"pg_backup_start('x','false'::text)", "pg_backup_start(unknown, text)"},
			{"pg_backup_stop('true'::text)", "pg_backup_stop(text)"},
			{"pg_backup_start(\"Label\"=>NULL)", "pg_backup_start(Label => unknown)"},
			{"pg_catalog.pg_backup_start(1)", "pg_catalog.pg_backup_start(integer)"},
		} {
			s := fixture.ImplicitError("SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
			s.SelectRows = true
			backupRoutes(t, p, b, c, s)
		}
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, tc := range []struct{ call, token, message string }{
				{"pg_backup_start(label=>'x',label=>'y')", "label=>'y'", `argument name "label" used more than once`},
				{"pg_backup_start(label=>'x',false)", "false", "positional argument cannot follow named argument"},
			} {
				q := prefix + "SELECT " + tc.call
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: position})
				s.SelectRows = true
				backupRoutes(t, p, b, c, s)
			}
		}
		for _, q := range []string{"SELECT pg_backup_start('x','bad')", "SELECT pg_backup_stop('bad')"} {
			pos := strconv.Itoa(strings.Index(q, "'bad'") + 1)
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "bad"`, Position: pos})
			s.SelectRows = true
			backupRoutes(t, p, b, c, s)
		}
	})
}
