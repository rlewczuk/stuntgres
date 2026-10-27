package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRoleRenameGrammar(t *testing.T) {
	for _, alias := range []string{"ROLE", "USER"} {
		for _, source := range []string{"Mixed", "set", "reset", "rename", `"current_user"`, "CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			query := "ALTER " + alias + " " + source + ` RENAME TO "New Role"`
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			s, ok := statements[0].(*RenameRole)
			if !ok || s.Name.Text != "New Role" || s.Name.Pos.Character != strings.Index(query, `"New Role"`)+1 || s.Pos.Character != 1 || s.Role.Pos.Character != len("ALTER "+alias+" ")+1 {
				t.Fatalf("%s: %#v", query, statements[0])
			}
			identity := ""
			name := strings.ToLower(source)
			switch source {
			case "CURRENT_USER", "CURRENT_ROLE":
				identity = "current"
				name = ""
			case "SESSION_USER":
				identity = "session"
				name = ""
			case `"current_user"`:
				name = "current_user"
			}
			if s.Role.Name != name || s.Role.Identity != identity {
				t.Fatal(query, s.Role)
			}
		}
	}
}

func TestRoleSettingsGrammar(t *testing.T) {
	for _, alias := range []string{"ROLE", "USER"} {
		for _, target := range []string{"set", "reset", "rename", "CURRENT_USER", "CURRENT_ROLE", "SESSION_USER", "ALL"} {
			for _, scope := range []string{"", ` IN DATABASE "Db"`} {
				prefix := "ALTER " + alias + " " + target + scope + " "
				for _, action := range []string{"SET application_name TO 'default'", "SET application_name = DEFAULT", "SET search_path TO public, 'Mixed Schema'", "SET custom.item FROM CURRENT", "RESET application_name", "RESET ALL", "SET role TO 'other'", "SET TIME ZONE 'UTC'", "RESET TIME ZONE"} {
					query := prefix + action
					statements, err := Parse(query)
					if err != nil || len(statements) != 1 {
						t.Fatal(query, statements, err)
					}
					s, ok := statements[0].(*AlterRoleSettings)
					if !ok || s.Pos.Character != 1 || s.All != (target == "ALL") || (s.Database != nil) != (scope != "") {
						t.Fatalf("%s: %#v", query, statements[0])
					}
					if s.Database != nil && (s.Database.Text != "Db" || s.Database.Pos.Character != strings.Index(query, `"Db"`)+1) {
						t.Fatal(query, s.Database)
					}
					if (s.Set == nil) == (s.Reset == nil) {
						t.Fatal("action discriminator", query, s)
					}
					if strings.HasPrefix(action, "RESET") {
						if s.Reset == nil || s.Reset.All != (action == "RESET ALL") || s.Reset.Pos.Character != len(prefix)+1 {
							t.Fatal(query, s.Reset)
						}
						continue
					}
					if s.Set == nil || s.Set.Local || s.Set.Pos.Character != len(prefix)+1 || s.FromCurrent != (action == "SET custom.item FROM CURRENT") {
						t.Fatal(query, s)
					}
					switch action {
					case "SET application_name TO 'default'":
						if s.Set.Default || !reflect.DeepEqual(s.Set.Values, []string{"default"}) {
							t.Fatal(query, s.Set)
						}
					case "SET application_name = DEFAULT":
						if !s.Set.Default || len(s.Set.Values) != 0 {
							t.Fatal(query, s.Set)
						}
					case "SET search_path TO public, 'Mixed Schema'":
						if !reflect.DeepEqual(s.Set.Values, []string{"public", "\"Mixed Schema\""}) {
							t.Fatal(query, s.Set)
						}
					case "SET custom.item FROM CURRENT":
						if s.Set.Name.Text != "custom.item" || len(s.Set.Values) != 0 {
							t.Fatal(query, s.Set)
						}
					}
				}
			}
		}
	}
}

func TestRoleSettingsWholeInput(t *testing.T) {
	for _, prefix := range []string{"ALTER ROLE set", "ALTER USER reset", "ALTER ROLE CURRENT_USER", "ALTER USER ALL IN DATABASE db"} {
		for _, action := range []string{"SET application_name TO", "SET application_name FROM", "SET application_name FROM 'current'", "SET application_name FROM CURRENT trailing", "RESET ALL trailing", "RESET application_name trailing", "SET application_name TO 'x' trailing"} {
			query := prefix + " " + action
			statements, err := Parse(query)
			var d *sqlstate.Error
			if statements != nil || !errors.As(err, &d) || d.Code != "42601" {
				t.Fatal(query, statements, err)
			}
		}
	}
	for _, query := range []string{"ALTER ROLE r RENAME", "ALTER ROLE r RENAME TO", "ALTER USER rename RENAME TO x trailing", "ALTER ROLE r IN DATABASE db RENAME TO x"} {
		statements, err := Parse(query)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(query, statements, err)
		}
	}
}
