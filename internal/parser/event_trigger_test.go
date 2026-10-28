package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestEventTriggerParser(t *testing.T) {
	statements, err := Parse(`CREATE EVENT TRIGGER "Event Name" ON ddl_command_start WHEN TAG IN ('create table','ALTER TABLE','create table') EXECUTE PROCEDURE public.event_callback(); ALTER EVENT TRIGGER "Event Name" DISABLE; ALTER EVENT TRIGGER "Event Name" ENABLE; DROP EVENT TRIGGER IF EXISTS "Event Name" CASCADE`)
	if err != nil || len(statements) != 4 {
		t.Fatal(statements, err)
	}
	create, ok := statements[0].(*CreateEventTrigger)
	if !ok || create.Name.Text != "Event Name" || create.Event.Text != "ddl_command_start" || create.Pos.Character != 1 || len(create.Filters) != 1 || create.Filters[0].Name.Text != "tag" || !reflect.DeepEqual(create.Filters[0].Values, []string{"create table", "ALTER TABLE", "create table"}) || len(create.Function.Parts) != 2 || create.Function.Parts[0].Text != "public" || create.Function.Parts[1].Text != "event_callback" {
		t.Fatal("lost positioned event declaration", create)
	}
	for i, want := range []byte{'D', 'O'} {
		alter, ok := statements[i+1].(*AlterEventTrigger)
		if !ok || alter.Name.Text != "Event Name" || alter.Action != "state" || alter.FiringMode != want || alter.Pos.Character <= 1 {
			t.Fatal("lost bounded state declaration", statements[i+1])
		}
	}
	drop, ok := statements[3].(*DropEventTrigger)
	if !ok || drop.Name.Text != "Event Name" || !drop.IfExists || drop.Mode != DropCascade {
		t.Fatal("lost global drop declaration", drop)
	}
	statements, err = Parse("CREATE EVENT TRIGGER e ON ddl_command_end EXECUTE FUNCTION f(); CREATE EVENT TRIGGER d ON ddl_command_start WHEN TAG IN ('CREATE TABLE') AND TAG IN ('ALTER TABLE') EXECUTE FUNCTION f(); CREATE TABLE event(event integer)")
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	if statements[0].(*CreateEventTrigger).Filters != nil || len(statements[1].(*CreateEventTrigger).Filters) != 2 || statements[2].(*CreateTable).Name.Parts[0].Text != "event" {
		t.Fatal("lost absent/repeated filters or unreserved EVENT")
	}
	for _, query := range []string{
		"CREATE OR REPLACE FUNCTION f() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
		"CREATE OR REPLACE FUNCTION f() RETURNS integer AS $$SELECT 1$$ LANGUAGE SQL",
		"CREATE OR REPLACE FUNCTION f() RETURNS integer RETURN 1",
		"CREATE OR REPLACE FUNCTION f() RETURNS integer LANGUAGE SQL RETURN 1",
		"CREATE OR REPLACE FUNCTION f() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
		function, ok := statements[0].(*CreateFunction)
		if !ok || !function.Replace || function.Procedure || function.Name.Parts[0].Text != "f" || function.Pos.Character != 1 {
			t.Fatal("replacement did not use ordinary function AST", function)
		}
	}
	for _, tc := range []struct {
		sql, action, target string
		mode                byte
		identity            bool
	}{
		{`ALTER EVENT TRIGGER "Event Name" ENABLE ALWAYS`, "state", "", 'A', false},
		{`ALTER EVENT TRIGGER "Event Name" ENABLE REPLICA`, "state", "", 'R', false},
		{`ALTER EVENT TRIGGER "Event Name" RENAME TO "New Name"`, "rename", "New Name", 0, false},
		{`ALTER EVENT TRIGGER "Event Name" OWNER TO CURRENT_ROLE`, "owner", "current_role", 0, true},
		{`ALTER EVENT TRIGGER "Event Name" OWNER TO CURRENT_USER`, "owner", "current_user", 0, true},
		{`ALTER EVENT TRIGGER "Event Name" OWNER TO SESSION_USER`, "owner", "session_user", 0, true},
		{`ALTER EVENT TRIGGER "Event Name" OWNER TO "CURRENT_USER"`, "owner", "CURRENT_USER", 0, false},
	} {
		nodes, err := Parse(tc.sql)
		if err != nil || len(nodes) != 1 {
			t.Fatal(tc.sql, err)
		}
		a := nodes[0].(*AlterEventTrigger)
		if a.Name.Text != "Event Name" || a.Action != tc.action || a.Target.Text != tc.target || a.FiringMode != tc.mode || a.OwnerIdentity != tc.identity || tc.target != "" && a.Target.Pos.Character <= a.Name.Pos.Character {
			t.Fatal("lost lifecycle operand or position", a)
		}
	}
	for _, query := range []string{
		"CREATE OR REPLACE EVENT TRIGGER e ON ddl_command_start EXECUTE FUNCTION f()",
		"CREATE EVENT TRIGGER s.e ON ddl_command_start EXECUTE FUNCTION f()",
		"CREATE EVENT TRIGGER e ON ddl_command_start EXECUTE FUNCTION f(1)",
		"CREATE EVENT TRIGGER e ON ddl_command_start WHEN TAG IN () EXECUTE FUNCTION f()",
		"CREATE EVENT TRIGGER e ON ddl_command_start EXECUTE FUNCTION f() trailing",
		"ALTER EVENT TRIGGER e ENABLE ALWAYS trailing",
		"ALTER EVENT TRIGGER e ENABLE REPLICA trailing",
		"ALTER EVENT TRIGGER e DISABLE REPLICA",
		"ALTER EVENT TRIGGER e DISABLE ALWAYS",
		"ALTER EVENT TRIGGER e ENABLE ORIGIN",
		"ALTER EVENT TRIGGER e ENABLE BOGUS",
		"ALTER EVENT TRIGGER e RENAME TO",
		"ALTER EVENT TRIGGER s.e ENABLE",
		"ALTER EVENT TRIGGER e OWNER TO u trailing",
	} {
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("unsupported/broken event syntax was accepted", query, err)
		}
	}
}
