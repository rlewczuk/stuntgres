package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyDiagnostics(t *testing.T) {
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA pk_left", 0))
		run(fixture.Exec("CREATE TABLE pkey_good(id integer PRIMARY KEY)", 0))
		held := pkeyHeld(t, p, c, pkeyCall)
		// REL_18_6 textToQualifiedNameList and RangeVarGetRelid supply
		// these independent complete records. Zero-valued optional fields
		// are assertions, not fields stripped for native parity. Each leaf
		// reports the complete actual record if the pinned oracle differs.
		for _, tc := range []struct{ name, relation, code, message string }{
			{"empty", "", "42602", "invalid name syntax"},
			{"whitespace", " \t\n", "42602", "invalid name syntax"},
			{"double_dot", "bad..name", "42602", "invalid name syntax"},
			{"unclosed_quote", `"unclosed`, "42602", "invalid name syntax"},
			{"missing_relation", "missing_pkey", "42P01", `relation "missing_pkey" does not exist`},
			{"missing_qualified_relation", "pk_left.missing_pkey", "42P01", `relation "pk_left.missing_pkey" does not exist`},
			{"missing_schema", "missing_pkey_schema.pkey_good", "3F000", `schema "missing_pkey_schema" does not exist`},
			{"missing_quoted_relation", `pk_left."Missing Pkey"`, "42P01", `relation "pk_left.Missing Pkey" does not exist`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Run("parameter", func(t *testing.T) {
					pkeyRoutes(t, p, b, c, &held, pkeyError(pkeyCall, tc.code, tc.message, tc.relation))
				})
				t.Run("literal", func(t *testing.T) {
					q := "SELECT * FROM pk_ext.dblink_get_pkey('" + strings.ReplaceAll(tc.relation, "'", "''") + "'::text) ORDER BY position"
					// VOLATILE: even constant relation names resolve at execute,
					// never as a Prepare-time relation dependency or fold.
					pkeyRoutes(t, p, b, c, nil, pkeyError(q, tc.code, tc.message))
				})
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pkeyRows([][]any{{int64(1), "id"}}, "pkey_good"))
			})
		}
		t.Run("savepoint_recovery", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("SAVEPOINT pkey_error", 0))
				s := pkeyError(pkeyCall, "42P01", `relation "missing_pkey" does not exist`, "missing_pkey")
				s.Prepared = prepared
				run(s)
				s = fixture.ImplicitError("SELECT 1", fixture.TransactionAborted())
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				run(fixture.Exec("ROLLBACK TO pkey_error", 0))
				run(fixture.Exec("RELEASE pkey_error", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pkeyRows([][]any{{int64(1), "id"}}, "pkey_good"))
			}
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
}
