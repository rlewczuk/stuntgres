package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceTriggerInsteadDiagnostics(t *testing.T) {
	cases := []struct{ name, q, code, message, detail string }{
		{"table", "CREATE TRIGGER bad INSTEAD OF INSERT ON t FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"t" is a table`, "Tables cannot have INSTEAD OF triggers."},
		{"partition", "CREATE TRIGGER bad INSTEAD OF INSERT ON pt FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"pt" is a table`, "Tables cannot have INSTEAD OF triggers."},
		{"foreign", "CREATE TRIGGER bad INSTEAD OF INSERT ON ft FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"ft" is a foreign table`, "Foreign tables cannot have INSTEAD OF triggers."},
		{"before", "CREATE TRIGGER bad BEFORE INSERT ON v FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"v" is a view`, "Views cannot have row-level BEFORE or AFTER triggers."},
		{"after", "CREATE TRIGGER bad AFTER DELETE ON v FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"v" is a view`, "Views cannot have row-level BEFORE or AFTER triggers."},
		{"truncate", "CREATE TRIGGER bad INSTEAD OF TRUNCATE ON v FOR EACH ROW EXECUTE FUNCTION f()", "42809", `"v" is a view`, "Views cannot have TRUNCATE triggers."},
		{"truncate-statement", "CREATE TRIGGER bad INSTEAD OF TRUNCATE ON v EXECUTE FUNCTION f()", "42809", `"v" is a view`, "Views cannot have TRUNCATE triggers."},
		{"omitted", "CREATE TRIGGER bad INSTEAD OF INSERT ON v EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers must be FOR EACH ROW", ""},
		{"statement", "CREATE TRIGGER bad INSTEAD OF UPDATE ON v FOR EACH STATEMENT EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers must be FOR EACH ROW", ""},
		{"when", "CREATE TRIGGER bad INSTEAD OF INSERT ON v FOR EACH ROW WHEN(true) EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers cannot have WHEN conditions", ""},
		{"when-field", "CREATE TRIGGER bad INSTEAD OF INSERT ON v FOR EACH ROW WHEN(NEW.id>0) EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers cannot have WHEN conditions", ""},
		{"when-subquery", "CREATE TRIGGER bad INSTEAD OF INSERT ON v FOR EACH ROW WHEN((SELECT true)) EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers cannot have WHEN conditions", ""},
		{"columns", "CREATE TRIGGER bad INSTEAD OF UPDATE OF id ON v FOR EACH ROW EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers cannot have column lists", ""},
		{"unknown-column", "CREATE TRIGGER bad INSTEAD OF UPDATE OF missing ON v FOR EACH ROW EXECUTE FUNCTION f()", "0A000", "INSTEAD OF triggers cannot have column lists", ""},
		{"kind-precedence", "CREATE TRIGGER bad INSTEAD OF UPDATE OF missing ON t WHEN(true) EXECUTE FUNCTION missing()", "42809", `"t" is a table`, "Tables cannot have INSTEAD OF triggers."},
		{"statement-precedence", "CREATE TRIGGER bad INSTEAD OF UPDATE OF missing ON v WHEN(true) EXECUTE FUNCTION missing()", "0A000", "INSTEAD OF triggers must be FOR EACH ROW", ""},
		{"when-precedence", "CREATE TRIGGER bad INSTEAD OF UPDATE OF missing ON v FOR EACH ROW WHEN(NEW.missing>0) EXECUTE FUNCTION missing()", "0A000", "INSTEAD OF triggers cannot have WHEN conditions", ""},
		{"repeat", "CREATE TRIGGER bad INSTEAD OF INSERT OR INSERT ON v FOR EACH ROW EXECUTE FUNCTION f()", "42601", `duplicate trigger events specified at or near "INSERT"`, ""},
		{"collision", "CREATE TRIGGER a INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION f()", "42710", `trigger "a" for relation "v" already exists`, ""},
		{"missing", "CREATE OR REPLACE TRIGGER a INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION missing()", "42883", "function missing() does not exist", ""},
		{"procedure", "CREATE TRIGGER bad INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION pr()", "42883", "function pr() does not exist", ""},
		{"procedure-keyword", "CREATE TRIGGER bad INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE PROCEDURE pr()", "42883", "function pr() does not exist", ""},
		{"integer", "CREATE OR REPLACE TRIGGER a INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION scalar()", "42P17", "function scalar must return type trigger", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c, "CREATE TABLE t(id integer)", "CREATE TABLE pt(id integer) PARTITION BY RANGE(id)", "CREATE VIEW v AS SELECT id FROM t", "CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=NEW.id+10; RETURN NEW; END$$", "CREATE FUNCTION scalar() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "CREATE PROCEDURE pr() LANGUAGE plpgsql AS $$BEGIN END$$", "CREATE TRIGGER a INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION f()")
				if tc.name == "foreign" {
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS io_fdw CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					userOperatorExec(t, p, b, c, "CREATE FOREIGN DATA WRAPPER io_fdw", "CREATE SERVER io_server FOREIGN DATA WRAPPER io_fdw", "CREATE FOREIGN TABLE ft(id integer) SERVER io_server")
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				if tc.name == "repeat" {
					d.Position = fixture.Position(41)
				}
				afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic(tc.q, d))
				afterStep(t, p, b, c, prepared, fixture.Select("INSERT INTO v VALUES(1) RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				afterStep(t, p, b, c, prepared, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
				if tc.name == "foreign" {
					userOperatorExec(t, p, b, c, "DROP FOREIGN TABLE ft", "DROP SERVER io_server", "DROP FOREIGN DATA WRAPPER io_fdw")
				}
			})
		})
	}
	for _, token := range []string{"SIDEWAYS", `"instead"`, "SELECT"} {
		t.Run("syntax-"+token, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "/* é */ CREATE TRIGGER bad " + token + " INSERT ON absent EXECUTE FUNCTION absent()"
				if token == "SELECT" {
					q = "/* é */ CREATE TRIGGER bad INSTEAD OF SELECT ON absent EXECUTE FUNCTION absent()"
				}
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near "+`"`+token+`"`)
				d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.ErrorDiagnostic(q, d)))
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message, detail, context string }{
		{"scalar", "RETURN 7;", "42804", "cannot return non-composite value from function returning composite type", "", "PL/pgSQL function io_bad() line 1 at RETURN"},
		{"width", "RETURN ROW(NEW.id);", "42804", "returned row structure does not match the structure of the triggering table", "Number of returned columns (1) does not match expected column count (2).", "PL/pgSQL function io_bad() during function exit"},
		{"type", "RETURN ROW(NEW.id,'bad'::text);", "42804", "returned row structure does not match the structure of the triggering table", "Returned type text does not match expected type integer in column \"v\" (position 2).", "PL/pgSQL function io_bad() during function exit"},
		{"fallthrough", "", "2F005", "control reached end of trigger procedure without RETURN", "", "PL/pgSQL function io_bad()"},
		{"body", "PERFORM 1/0; RETURN NEW;", "22012", "division by zero", "", "SQL statement \"SELECT 1/0\"\nPL/pgSQL function io_bad() line 1 at PERFORM"},
	} {
		t.Run("runtime-"+tc.name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION io_bad() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN "+tc.body+" END$$", "CREATE OR REPLACE TRIGGER body INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_bad()")
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				d.Context = tc.context
				afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic("INSERT INTO io_view VALUES(1,10)", d))
				afterStep(t, p, b, c, prepared, triggerRows("SELECT id,v FROM io_base"))
			})
		})
	}
	t.Run("base-identity", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,5)", 1))
			d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "io_base_pkey"`)
			d.Detail = "Key (id)=(1) already exists."
			d.Schema = "public"
			d.Table = "io_base"
			d.Constraint = "io_base_pkey"
			d.Context = "SQL statement \"INSERT INTO io_base VALUES(NEW.id,NEW.v+1)\"\nPL/pgSQL function io_body() line 2 at SQL statement"
			afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic("INSERT INTO io_view VALUES(1,10)", d))
			afterStep(t, p, b, c, prepared, triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(5)}))
			afterStep(t, p, b, c, prepared, insteadLog("SELECT op,id,ov,nv FROM io_log"))
		})
	})
	t.Run("prepare-is-quiet", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			insteadSetup(t, p, b, c)
			q := "CREATE TRIGGER extra INSTEAD OF UPDATE ON io_view FOR EACH ROW EXECUTE FUNCTION io_body()"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM information_schema.triggers WHERE trigger_name='extra'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, insteadLog("SELECT op,id,ov,nv FROM io_log"))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM information_schema.triggers WHERE trigger_name='extra'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	})
	t.Run("utf8-expression", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			q := "/* é */ INSERT INTO io_view VALUES(1,10) RETURNING absent"
			d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "absent")]) + 1)
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			afterStep(t, p, b, c, prepared, s)
			afterStep(t, p, b, c, prepared, triggerRows("SELECT id,v FROM io_base"))
			afterStep(t, p, b, c, prepared, insteadLog("SELECT op,id,ov,nv FROM io_log"))
		})
	})
}
