package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const externalCustomHandler = " HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator"

func externalHandlerBoundary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	q := "CREATE LANGUAGE " + name + externalCustomHandler
	if b.PG {
		routineExec(t, p, b, c, q)
		// Languages are database-wide; the fixture's public reset cannot clean
		// them. Callers drop any dependent functions before this deferred drop.
		t.Cleanup(func() {
			if _, err := b.DB.ExecContext(p.Ctx, "DROP LANGUAGE "+name); err != nil {
				t.Error(err)
			}
		})
	} else {
		externalLanguageError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("0A000", "custom procedural language handlers are unsupported in embedded mode"), false)
	}
}

func TestReferenceRoutineExternalLanguagesHandlerDeclarations(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ext_handler_reader"})
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageInventory(t, p, b, c)
		routineExec(t, p, b, c, "CREATE ROLE ext_handler_reader", "SET SESSION AUTHORIZATION ext_handler_reader")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{
			"CREATE LANGUAGE ext_handler_denied HANDLER ext_missing_handler",
			"CREATE TRUSTED LANGUAGE ext_handler_denied HANDLER pg_catalog.plpgsql_call_handler",
			"CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE ext_handler_denied" + externalCustomHandler,
		} {
			externalLanguageError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "must be superuser to create custom procedural language"), false)
		}
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT lanname FROM pg_catalog.pg_language WHERE lanname='ext_handler_denied'", []string{"lanname"}, []string{"NAME"}, nil))
		externalHandlerBoundary(t, p, b, c, "ext_custom")
		q := "CREATE FUNCTION ext_custom_value(integer) RETURNS integer LANGUAGE ext_custom AS $$BEGIN RETURN $1+1; END$$"
		if b.PG {
			routineExec(t, p, b, c, q)
			routineValue(t, p, b, c, "SELECT ext_custom_value(4) AS v", "v", "INT4", int64(5))
			routineExec(t, p, b, c, "DROP FUNCTION ext_custom_value(integer)")
			// Replacement/flags exercise the same real handler, not a fake
			// native language registration.
			routineExec(t, p, b, c, "CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE ext_custom"+externalCustomHandler)
		} else {
			externalLanguageMissing(t, p, b, c, q, "ext_custom")
			externalLanguageMissingCall(t, p, b, c, "ext_custom_value")
			externalLanguageError(t, p, b, c, "CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE ext_custom"+externalCustomHandler, fixture.ControlsReferenceDiagnostic("0A000", "custom procedural language handlers are unsupported in embedded mode"), false)
		}
		for _, q := range []string{
			"CREATE LANGUAGE ext_bad HANDLER pg_catalog.plpgsql_call_handler trailing",
			"CREATE LANGUAGE ext_bad HANDLER pg_catalog.plpgsql_call_handler VALIDATOR pg_catalog.plpgsql_validator INLINE pg_catalog.plpgsql_inline_handler",
		} {
			token := "trailing"
			if strings.Contains(q, " INLINE ") {
				token = "INLINE"
			}
			d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \""+token+"\"")
			d.Position = strconv.Itoa(strings.Index(q, token) + 1)
			externalLanguageError(t, p, b, c, q, d, false)
		}
	})
}
