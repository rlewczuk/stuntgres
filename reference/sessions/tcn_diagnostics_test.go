package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTcnDiagnostics(t *testing.T) {
	cases := []struct {
		name, table, trigger, query, code, message string
		detail, schema, column                     string
	}{
		{name: "ordinary", query: "SELECT triggered_change_notification()", code: "39P01", message: "triggered_change_notification: must be called as trigger"},
		{name: "before_precedence", table: "CREATE TABLE tcn_bad(id int)", trigger: "BEFORE INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must be called after the change"},
		{name: "instead_of", table: "CREATE TABLE tcn_base(id int PRIMARY KEY)", trigger: "INSTEAD OF INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must be called after the change"},
		{name: "statement_precedence", table: "CREATE TABLE tcn_bad(id int)", trigger: "AFTER INSERT ON tcn_bad FOR EACH STATEMENT", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must be called for each row"},
		{name: "argument_precedence", table: "CREATE TABLE tcn_bad(id int)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must not be called with more than one parameter"},
		{name: "no_primary_key", table: "CREATE TABLE tcn_bad(id int)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must be called on a table with a primary key"},
		{name: "unique_is_not_primary", table: "CREATE TABLE tcn_bad(id int UNIQUE)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "39P01", message: "triggered_change_notification: must be called on a table with a primary key"},
		{name: "no_pk_update", table: "CREATE TABLE tcn_bad(id int)", trigger: "AFTER UPDATE ON tcn_bad FOR EACH ROW", query: "UPDATE tcn_bad SET id=2", code: "39P01", message: "triggered_change_notification: must be called on a table with a primary key"},
		{name: "no_pk_delete", table: "CREATE TABLE tcn_bad(id int)", trigger: "AFTER DELETE ON tcn_bad FOR EACH ROW", query: "DELETE FROM tcn_bad", code: "39P01", message: "triggered_change_notification: must be called on a table with a primary key"},
		{name: "empty_channel", table: "CREATE TABLE tcn_bad(id int PRIMARY KEY)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "22023", message: "channel name cannot be empty"},
		{name: "long_channel", table: "CREATE TABLE tcn_bad(id int PRIMARY KEY)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "22023", message: "channel name too long"},
		{name: "multibyte_channel", table: "CREATE TABLE tcn_bad(id int PRIMARY KEY)", trigger: "AFTER INSERT ON tcn_bad FOR EACH ROW", query: "INSERT INTO tcn_bad VALUES (1)", code: "22023", message: "channel name too long"},
		{name: "null_key", table: "CREATE TABLE tcn_null(id int PRIMARY KEY,note text)", trigger: "AFTER INSERT ON tcn_null FOR EACH ROW", query: "INSERT INTO tcn_null VALUES (NULL,'x')", code: "23502", message: `null value in column "id" of relation "tcn_null" violates not-null constraint`, detail: "Failing row contains (null, x).", schema: "public", column: "id"},
		{name: "payload_boundary", table: "CREATE TABLE tcn_size(id text PRIMARY KEY)", trigger: "AFTER INSERT ON tcn_size FOR EACH ROW", query: "INSERT INTO tcn_size VALUES (repeat('x',7980))", code: "22023", message: "payload string too long"},
		{name: "quote_expansion", table: "CREATE TABLE tcn_size(id text PRIMARY KEY)", trigger: "AFTER INSERT ON tcn_size FOR EACH ROW", query: "INSERT INTO tcn_size VALUES (repeat('''',3990))", code: "22023", message: "payload string too long"},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			name := c.name + "/direct"
			if prepared {
				name = c.name + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					sender, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer sender.Close()
					receiver := sessionMessageConn(t, p, b)
					sessionMessageSQL(t, p, b, receiver, "LISTEN tcn")
					sessionMessageSQL(t, p, b, sender, "CREATE EXTENSION tcn")
					if c.table != "" {
						sessionMessageSQL(t, p, b, sender, c.table)
					}
					if c.name == "instead_of" {
						sessionMessageSQL(t, p, b, sender, "CREATE VIEW tcn_bad AS SELECT id FROM tcn_base")
					}
					if c.name == "no_pk_update" || c.name == "no_pk_delete" {
						fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec("INSERT INTO tcn_bad VALUES (1)", 1))
					}
					if c.trigger != "" {
						args := ""
						switch c.name {
						case "before_precedence", "statement_precedence", "argument_precedence":
							args = "'a','b'"
						case "empty_channel":
							args = "''"
						case "long_channel":
							args = "'" + strings.Repeat("x", 64) + "'"
						case "multibyte_channel":
							args = "'" + strings.Repeat("é", 32) + "'"
						}
						sessionMessageSQL(t, p, b, sender, "CREATE TRIGGER changes "+c.trigger+" EXECUTE FUNCTION triggered_change_notification("+args+")")
					}
					want := fixture.ControlsReferenceDiagnostic(c.code, c.message)
					want.Detail, want.Schema, want.Column = c.detail, c.schema, c.column
					if c.name == "null_key" {
						want.Table = "tcn_null"
					}
					step := fixture.ErrorDiagnostic(c.query, want)
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, sender, b.Recorder, step)
					if c.table != "" {
						table, wantRows := "tcn_bad", int64(0)
						if c.name == "instead_of" {
							table = "tcn_base"
						}
						if c.name == "no_pk_update" || c.name == "no_pk_delete" {
							wantRows = 1
						}
						if c.name == "null_key" {
							table = "tcn_null"
						}
						if c.name == "payload_boundary" || c.name == "quote_expansion" {
							table = "tcn_size"
						}
						fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{wantRows}}))
						if wantRows == 1 {
							fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Select("SELECT id FROM tcn_bad", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						}
					}
					sessionMessageSQL(t, p, b, sender, "CREATE TABLE tcn_error_marker(id int PRIMARY KEY)")
					sessionMessageSQL(t, p, b, sender, "CREATE TRIGGER marker AFTER INSERT ON tcn_error_marker FOR EACH ROW EXECUTE FUNCTION triggered_change_notification()")
					fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec("INSERT INTO tcn_error_marker VALUES (9)", 1))
					sessionMessageReceive(t, p, b, receiver, "tcn", `"tcn_error_marker",I,"id"='9'`)
				})
			})
		}
	}
	t.Run("nested_ordinary_call", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			sender := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, sender, "CREATE EXTENSION tcn")
			sessionMessageSQL(t, p, b, sender, "CREATE TABLE tcn_nested_row(id int PRIMARY KEY)")
			sessionMessageSQL(t, p, b, sender, "CREATE FUNCTION tcn_nested() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM triggered_change_notification(); RETURN NEW; END $$")
			sessionMessageSQL(t, p, b, sender, "CREATE TRIGGER nested BEFORE INSERT ON tcn_nested_row FOR EACH ROW EXECUTE FUNCTION tcn_nested()")
			want := fixture.ControlsReferenceDiagnostic("39P01", "triggered_change_notification: must be called as trigger")
			want.Context = "SQL statement \"SELECT triggered_change_notification()\"\nPL/pgSQL function tcn_nested() line 1 at PERFORM"
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic("INSERT INTO tcn_nested_row VALUES (1)", want)
				if prepared {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, sender, b.Recorder, step)
			}
		})
	})
}
