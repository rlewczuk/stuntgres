package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete diagnostics observed on the pinned PostgreSQL 18.6 oracle.
func TestReferenceTriggerCoreDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, setup, query, state, message string }{
		{"scalar", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "INSERT INTO tr_data VALUES(1,5)", "42804", "cannot return non-composite value from function returning composite type"},
		{"width", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN ROW(NEW.id); END$$", "INSERT INTO tr_data VALUES(1,5)", "42804", "returned row structure does not match the structure of the triggering table"},
		{"type", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN ROW(NEW.id,'bad'::text); END$$", "INSERT INTO tr_data VALUES(1,5)", "42804", "returned row structure does not match the structure of the triggering table"},
		{"fallthrough", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN END$$", "INSERT INTO tr_data VALUES(1,5)", "2F005", "control reached end of trigger procedure without RETURN"},
		{"insert_error", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := 100/(NEW.id-2); RETURN NEW; END$$", "INSERT INTO tr_data VALUES(1,5),(2,5),(3,5)", "22012", "division by zero"},
		{"direct", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "SELECT tr_probe()", "0A000", "trigger functions can only be called as triggers"},
		{"missing_field", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := OLD.missing; RETURN NEW; END$$", "INSERT INTO tr_data VALUES(1,5)", "42703", "record \"old\" has no field \"missing\""},
		{"declared_argument", "", "CREATE FUNCTION tr_bad_arg(x integer) RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "42P13", "trigger functions cannot have declared arguments"},
		{"when_old", "", "CREATE TRIGGER bad BEFORE INSERT ON tr_data FOR EACH ROW WHEN(OLD.v=1) EXECUTE FUNCTION tr_double()", "42P17", "INSERT trigger's WHEN condition cannot reference OLD values"},
		{"when_new", "", "CREATE TRIGGER bad BEFORE DELETE ON tr_data FOR EACH ROW WHEN(NEW.v=1) EXECUTE FUNCTION tr_double()", "42P17", "DELETE trigger's WHEN condition cannot reference NEW values"},
		{"when_subquery", "", "CREATE TRIGGER bad BEFORE INSERT ON tr_data FOR EACH ROW WHEN((SELECT true)) EXECUTE FUNCTION tr_double()", "0A000", "cannot use subquery in trigger WHEN condition"},
		{"missing_function", "", "CREATE OR REPLACE TRIGGER a BEFORE UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_missing()", "42883", "function tr_missing() does not exist"},
		{"dependency", "CREATE FUNCTION tr_probe() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "DROP FUNCTION tr_probe()", "2BP01", "cannot drop function tr_probe() because other objects depend on it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerSetup(t, p, b, c)
				if tc.setup != "" {
					userOperatorExec(t, p, b, c, tc.setup, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_probe()")
				}
				d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
				switch tc.name {
				case "scalar":
					d.Context = "PL/pgSQL function tr_probe() line 1 at RETURN"
				case "width":
					d.Detail = "Number of returned columns (1) does not match expected column count (2)."
					d.Context = "PL/pgSQL function tr_probe() during function exit"
				case "type":
					d.Detail = "Returned type text does not match expected type integer in column \"v\" (position 2)."
					d.Context = "PL/pgSQL function tr_probe() during function exit"
				case "fallthrough":
					d.Context = "PL/pgSQL function tr_probe()"
				case "insert_error":
					d.Context = "PL/pgSQL function tr_probe() line 1 at assignment"
				case "direct":
					d.Context = "compilation of PL/pgSQL function \"tr_probe\" near line 1"
				case "missing_field":
					d.Context = "PL/pgSQL assignment \"NEW.v := OLD.missing\"\nPL/pgSQL function tr_probe() line 1 at assignment"
				case "declared_argument":
					d.Hint = "The arguments of the trigger can be accessed through TG_NARGS and TG_ARGV instead."
					d.Context = "compilation of PL/pgSQL function \"tr_bad_arg\" near line 1"
				case "when_old", "when_new", "when_subquery":
					d.Position = "63"
				case "dependency":
					d.Detail = "trigger a on table tr_data depends on function tr_probe()"
					d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				}
				plpgsqlError(t, p, c, tc.query, tc.name == "direct", d)
			})
		})
	}
}
