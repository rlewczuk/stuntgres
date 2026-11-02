package parser

import (
	"strings"
	"testing"
)

func TestCreateExtensionCascadeOptions(t *testing.T) {
	for _, tc := range []struct {
		query                 string
		names, tokens, values []string
	}{
		{"CREATE EXTENSION earthdistance CASCADE", []string{"cascade"}, []string{"CASCADE"}, []string{""}},
		{"CREATE EXTENSION earthdistance WITH CASCADE VERSION '1.2' SCHEMA addons", []string{"cascade", "version", "schema"}, []string{"CASCADE", "VERSION", "SCHEMA"}, []string{"", "1.2", "addons"}},
		{"CREATE EXTENSION earthdistance SCHEMA addons CASCADE VERSION '1.2'", []string{"schema", "cascade", "version"}, []string{"SCHEMA", "CASCADE", "VERSION"}, []string{"addons", "", "1.2"}},
		{"CREATE EXTENSION IF NOT EXISTS earthdistance VERSION '1.2' CASCADE SCHEMA addons", []string{"version", "cascade", "schema"}, []string{"VERSION", "CASCADE", "SCHEMA"}, []string{"1.2", "", "addons"}},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		create, ok := statements[0].(*CreateExtension)
		if !ok || create.Name.Text != "earthdistance" || len(create.Options) != len(tc.names) || create.IfNotExists != strings.Contains(tc.query, "IF NOT EXISTS") {
			t.Fatalf("lost CREATE identity/options: %#v", statements[0])
		}
		for i, option := range create.Options {
			if option.Name != tc.names[i] || option.Value.Text != tc.values[i] || option.Pos.Character != strings.Index(tc.query, tc.tokens[i])+1 {
				t.Fatal("lost ordered option/source position", tc.query, i, option)
			}
		}
	}
	query := "CREATE EXTENSION earthdistance CASCADE CASCADE"
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	create := statements[0].(*CreateExtension)
	if len(create.Options) != 2 || create.Options[0].Name != "cascade" || create.Options[1].Name != "cascade" || create.Options[1].Pos.Character != 40 {
		t.Fatal("semantic duplicate/source position was erased", create.Options)
	}
	for _, query := range []string{"CREATE EXTENSION earthdistance CASCADE trailing", "CREATE EXTENSION earthdistance WITH CASCADE VERSION", "CREATE EXTENSION earthdistance CASCADE SCHEMA", "CREATE EXTENSION earthdistance CASCADE; trailing"} {
		if statements, err := Parse(query); err == nil || len(statements) != 0 {
			t.Fatal("accepted a valid prefix/incomplete options", query, statements, err)
		}
	}
}
