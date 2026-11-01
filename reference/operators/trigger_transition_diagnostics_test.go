package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerTransitionDiagnostics(t *testing.T) {
	cases := []struct{ name, clause, code, message, detail, hint string }{
		{"before", "BEFORE INSERT ON tt REFERENCING NEW TABLE x", "42P17", "transition table name can only be specified for an AFTER trigger", "", ""},
		{"truncate", "AFTER TRUNCATE ON tt REFERENCING OLD TABLE x", "0A000", "TRUNCATE triggers with transition tables are not supported", "", ""},
		{"events", "AFTER INSERT OR UPDATE ON tt REFERENCING NEW TABLE x", "0A000", "transition tables cannot be specified for triggers with more than one event", "", ""},
		{"columns", "AFTER UPDATE OF id ON tt REFERENCING NEW TABLE x", "0A000", "transition tables cannot be specified for triggers with column lists", "", ""},
		{"missing-column", "AFTER UPDATE OF missing ON tt REFERENCING NEW TABLE x", "0A000", "transition tables cannot be specified for triggers with column lists", "", ""},
		{"insert-old", "AFTER INSERT ON tt REFERENCING OLD TABLE x", "42P17", "OLD TABLE can only be specified for a DELETE or UPDATE trigger", "", ""},
		{"delete-new", "AFTER DELETE ON tt REFERENCING NEW TABLE x", "42P17", "NEW TABLE can only be specified for an INSERT or UPDATE trigger", "", ""},
		{"duplicate-old", "AFTER UPDATE ON tt REFERENCING OLD TABLE x OLD TABLE y", "42P17", "OLD TABLE cannot be specified multiple times", "", ""},
		{"duplicate-new", "AFTER UPDATE ON tt REFERENCING NEW TABLE x NEW TABLE y", "42P17", "NEW TABLE cannot be specified multiple times", "", ""},
		{"same-alias", "AFTER UPDATE ON tt REFERENCING OLD TABLE x NEW TABLE x", "42P17", "OLD TABLE name and NEW TABLE name cannot be the same", "", ""},
		{"folded-alias", "AFTER UPDATE ON tt REFERENCING NEW TABLE X OLD TABLE x", "42P17", "OLD TABLE name and NEW TABLE name cannot be the same", "", ""},
		{"old-row", "AFTER UPDATE ON tt REFERENCING OLD ROW AS x", "0A000", "ROW variable naming in the REFERENCING clause is not supported", "", "Use OLD TABLE or NEW TABLE for naming transition tables."},
		{"new-row", "AFTER UPDATE ON tt REFERENCING NEW ROW x", "0A000", "ROW variable naming in the REFERENCING clause is not supported", "", "Use OLD TABLE or NEW TABLE for naming transition tables."},
		{"view", "AFTER INSERT ON tt_view REFERENCING NEW TABLE x", "42809", `"tt_view" is a view`, "Triggers on views cannot have transition tables.", ""},
		{"foreign", "AFTER INSERT ON tt_foreign REFERENCING NEW TABLE x", "42809", `"tt_foreign" is a foreign table`, "Triggers on foreign tables cannot have transition tables.", ""},
		{"row-view-precedence", "AFTER INSERT ON tt_view REFERENCING NEW ROW x", "0A000", "ROW variable naming in the REFERENCING clause is not supported", "", "Use OLD TABLE or NEW TABLE for naming transition tables."},
		{"row-foreign-precedence", "BEFORE UPDATE OF missing ON tt_foreign REFERENCING NEW ROW x", "0A000", "ROW variable naming in the REFERENCING clause is not supported", "", "Use OLD TABLE or NEW TABLE for naming transition tables."},
		{"collision-later-row", "AFTER UPDATE ON tt REFERENCING OLD TABLE x NEW TABLE x OLD ROW y", "0A000", "ROW variable naming in the REFERENCING clause is not supported", "", "Use OLD TABLE or NEW TABLE for naming transition tables."},
		{"duplicate-before-row", "AFTER UPDATE ON tt REFERENCING OLD TABLE x OLD TABLE y NEW ROW z", "42P17", "OLD TABLE cannot be specified multiple times", "", ""},
		{"partitioned-row", "AFTER INSERT ON tt_partitioned REFERENCING NEW TABLE x FOR EACH ROW", "0A000", `"tt_partitioned" is a partitioned table`, "ROW triggers with transition tables are not supported on partitioned tables.", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE tt(id integer)", "CREATE VIEW tt_view AS SELECT id FROM tt", "CREATE TABLE tt_log(id integer)",
					"CREATE FUNCTION tt_valid() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_log SELECT id FROM x; RETURN NULL; END$$",
					"CREATE TRIGGER a AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_valid()")
				if tc.name == "partitioned-row" {
					userOperatorExec(t, p, b, c, "CREATE TABLE tt_partitioned(id integer) PARTITION BY RANGE(id)")
				}
				if strings.Contains(tc.name, "foreign") {
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS tt_fdw CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					userOperatorExec(t, p, b, c, "CREATE FOREIGN DATA WRAPPER tt_fdw", "CREATE SERVER tt_server FOREIGN DATA WRAPPER tt_fdw", "CREATE FOREIGN TABLE tt_foreign(id integer) SERVER tt_server")
				}
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Hint = tc.detail, tc.hint
				// Missing routine is deliberate: transition restrictions precede lookup.
				q := "CREATE OR REPLACE TRIGGER a " + tc.clause + " EXECUTE FUNCTION missing()"
				run(fixture.ErrorDiagnostic(q, d))
				run(fixture.Select("SELECT id FROM tt_log", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT tgname,tgoldtable,tgnewtable FROM pg_trigger WHERE tgrelid='tt'::regclass", []string{"tgname", "tgoldtable", "tgnewtable"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"a", nil, "x"}}))
				if strings.Contains(tc.clause, "ON tt_view ") || strings.Contains(tc.clause, "ON tt_foreign ") || tc.name == "partitioned-row" {
					target := "tt_view"
					if strings.Contains(tc.name, "foreign") {
						target = "tt_foreign"
					}
					if tc.name == "partitioned-row" {
						target = "tt_partitioned"
					}
					run(fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='"+target+"'::regclass", []string{"tgname"}, []string{"NAME"}, nil))
				}
				run(fixture.Exec("INSERT INTO tt VALUES(1)", 1))
				run(fixture.Select("SELECT id FROM tt_log", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	t.Run("constraint-character-position", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tt(id integer)")
			q := `CREATE CONSTRAINT TRIGGER "é名" AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW EXECUTE FUNCTION missing()`
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "REFERENCING"`)
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "REFERENCING")]) + 1)
			afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic(q, d))
			afterStep(t, p, b, c, prepared, fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='tt'::regclass", []string{"tgname"}, []string{"NAME"}, nil))
		})
	})
	t.Run("quoted-distinct", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tt(id integer)", "CREATE TABLE tt_log(o integer,n integer)", `CREATE FUNCTION tt_valid() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_log SELECT a.id,b.id FROM "X" a CROSS JOIN x b; RETURN NULL; END$$`)
			afterStep(t, p, b, c, prepared, fixture.Exec("INSERT INTO tt VALUES(1)", 1))
			afterStep(t, p, b, c, prepared, fixture.Exec(`CREATE TRIGGER a AFTER UPDATE ON tt REFERENCING NEW TABLE AS x OLD TABLE "X" EXECUTE FUNCTION tt_valid()`, 0))
			afterStep(t, p, b, c, prepared, fixture.Exec("UPDATE tt SET id=2", 1))
			afterStep(t, p, b, c, prepared, fixture.Select("SELECT o,n FROM tt_log", []string{"o", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		})
	})
}
