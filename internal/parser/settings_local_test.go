package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSettingsLocalScopes(t *testing.T) {
	for _, tc := range []struct {
		sql, name, value string
		local            bool
	}{
		{"SET LOCAL application_name='x'", "application_name", "x", true},
		{"SET LOCAL local='x'", "local", "x", true},
		{"SET LOCAL role.deep='x'", "role.deep", "x", true},
		{"SET local='x'", "local", "x", false},
		{"SET SESSION local='x'", "local", "x", false},
		{"SET schema.deep='x'", "schema.deep", "x", false},
		{"SET SCHEMA 'Mixed Name'", "search_path", `"Mixed Name"`, false},
		{"SET LOCAL SCHEMA 'a,b'", "search_path", `"a,b"`, true},
		{"SET SESSION SCHEMA 'a\"b'", "search_path", `"a""b"`, false},
		{"SET SCHEMA ''", "search_path", `""`, false},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		s := statements[0].(*Set)
		if s.Local != tc.local || s.Name.Text != tc.name || len(s.Values) != 1 || s.Values[0] != tc.value {
			t.Fatal(tc.sql, s)
		}
	}
	for _, sql := range []string{"SET SCHEMA public", "SET SCHEMA DEFAULT"} {
		_, err := Parse(sql)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" || d.Position != "12" {
			t.Fatal(sql, err)
		}
	}
}
