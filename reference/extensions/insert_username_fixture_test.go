package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations in this family come from REL_18_6 contrib/spi/insert_username.c.
// Empty diagnostic fields and wrapper contexts await the root's pinned oracle run.
func insertUsernameCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"iu_owner", "iu_writer", "iu_other", "iu_renamed"})
	for _, name := range []string{"iu_ext", "iu_moved", "iu_smoke_ext"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			defer func() {
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET search_path"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(q, err) }
				}
			}()
			body(t, p, b, c)
		})
	}
}

func insertUsernameRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func insertUsernameSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := insertUsernameRun(t, p, b, c)
	for _, q := range []string{
		"CREATE ROLE iu_owner", "CREATE ROLE iu_writer", "CREATE ROLE iu_other",
		"CREATE SCHEMA iu_ext", "CREATE EXTENSION insert_username SCHEMA iu_ext VERSION '1.0'",
		"GRANT USAGE ON SCHEMA public, iu_ext TO iu_owner, iu_writer, iu_other",
		"CREATE TABLE iu_data(id integer, actor text NOT NULL, note text)",
		"ALTER TABLE iu_data OWNER TO iu_owner",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON iu_data TO iu_writer, iu_other",
		"CREATE TRIGGER iu_stamp BEFORE INSERT OR UPDATE ON iu_data FOR EACH ROW EXECUTE FUNCTION iu_ext.insert_username('actor')",
	} { run(fixture.Exec(q, 0)) }
}

func insertUsernameError(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	return s
}

func insertUsernameBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	run := insertUsernameRun(t, p, b, c)
	run(s)
	run(fixture.Prepared(s))
}

func insertUsernameRows(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"id", "actor", "note"}, []string{"INT4", "TEXT", "TEXT"}, rows, args...)
}
