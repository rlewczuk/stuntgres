package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const externalCustomHandler = " HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator"

func externalHandlerRegistration(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	q := "CREATE LANGUAGE " + name + externalCustomHandler
	routineExec(t, p, b, c, q)
	// Languages are database-wide; schema reset cannot clean them. Explicit
	// function retirement is asserted by callers; CASCADE also cleans failures.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := b.DB.ExecContext(ctx, "DROP LANGUAGE "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	externalLanguageError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42710", fmt.Sprintf("language %q already exists", name)), false)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT l.lanispl,l.lanpltrusted,h.proname,i.proname,v.proname FROM pg_language l JOIN pg_proc h ON h.oid=l.lanplcallfoid JOIN pg_proc i ON i.oid=l.laninline JOIN pg_proc v ON v.oid=l.lanvalidator WHERE l.lanname=$1", []string{"lanispl", "lanpltrusted", "proname", "proname", "proname"}, []string{"BOOL", "BOOL", "NAME", "NAME", "NAME"}, [][]any{{true, false, "plpgsql_call_handler", "plpgsql_inline_handler", "plpgsql_validator"}}, name))
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
		externalHandlerRegistration(t, p, b, c, "ext_custom")
		q := "CREATE FUNCTION ext_custom_value(integer) RETURNS integer LANGUAGE ext_custom AS $$BEGIN RETURN $1+1; END$$"
		routineExec(t, p, b, c, q)
		routineValue(t, p, b, c, "SELECT ext_custom_value(4) AS v", "v", "INT4", int64(5))
		routineExec(t, p, b, c, "DROP FUNCTION ext_custom_value(integer)")
		externalLanguageMissingCall(t, p, b, c, "ext_custom_value")
		// Replacement/flags select the same genuine implemented handlers.
		routineExec(t, p, b, c, "CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE ext_custom"+externalCustomHandler)
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
