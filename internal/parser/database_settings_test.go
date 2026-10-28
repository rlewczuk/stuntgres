package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDatabaseSettingsGrammar(t *testing.T) {
	for _, target := range []struct{ sql, decoded string }{{"Mixed", "mixed"}, {`"Mixed db"`, "Mixed db"}, {`"é"`, "é"}, {"set", "set"}} {
		for _, action := range []string{"SET application_name TO 'default'", "SET application_name = DEFAULT", "SET search_path TO public, 'Mixed Schema'", "SET custom.item FROM CURRENT", "RESET application_name", "RESET ALL", "SET TIME ZONE 'UTC'", "RESET TIME ZONE"} {
			query := "ALTER DATABASE " + target.sql + " " + action
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			s, ok := statements[0].(*AlterDatabaseSettings)
			if !ok || s.Database.Text != target.decoded || s.Database.Pos.Character != 16 || s.Pos.Character != 1 || (s.Set == nil) == (s.Reset == nil) {
				t.Fatalf("%s: %#v", query, statements[0])
			}
			prefix := "ALTER DATABASE " + target.sql + " "
			position := len([]rune(prefix)) + 1
			if strings.HasPrefix(action, "RESET") {
				if s.Reset == nil || s.Reset.All != (action == "RESET ALL") || s.Reset.Pos.Character != position {
					t.Fatal(query, s.Reset)
				}
				continue
			}
			if s.Set == nil || s.Set.Local || s.Set.Pos.Character != position || s.FromCurrent != strings.Contains(action, "FROM CURRENT") {
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
				if !reflect.DeepEqual(s.Set.Values, []string{"public", `"Mixed Schema"`}) {
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

func TestDatabaseSettingsWholeInput(t *testing.T) {
	for _, action := range []string{"SET application_name TO", "SET application_name FROM", "SET application_name FROM 'current'", "SET application_name FROM CURRENT trailing", "RESET ALL trailing", "RESET application_name trailing", "SET application_name TO 'x' trailing", "SET LOCAL application_name TO 'x'"} {
		query := `ALTER DATABASE "é" ` + action
		statements, err := Parse(query)
		var d *sqlstate.Error
		if statements != nil || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(query, statements, err)
		}
	}
}
