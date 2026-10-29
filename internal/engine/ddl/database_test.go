package ddl

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// CheckCreateDatabase asks exists about a non-default template and, only after
// every option check passed, about the new name.
func TestCheckCreateDatabaseOrder(t *testing.T) {
	valid := CreateDatabaseOptions{Template: "template1", Encoding: 6, Collate: "C", CType: "POSIX", AllowConnections: true, ConnectionLimit: -1}
	registered := func(names ...string) func(string) bool {
		return func(name string) bool {
			for _, n := range names {
				if n == name {
					return true
				}
			}
			return false
		}
	}
	withTemplate := func(name string) CreateDatabaseOptions { o := valid; o.Template = name; return o }
	cases := []struct {
		label   string
		name    string
		options CreateDatabaseOptions
		exists  func(string) bool
		code    string
	}{
		{"valid", "fresh", valid, registered("template1"), ""},
		{"missing template", "fresh", withTemplate("absent"), registered(), "3D000"},
		{"other template", "fresh", withTemplate("postgres"), registered("postgres"), "0A000"},
		{"template0 before encoding", "fresh", func() CreateDatabaseOptions { o := withTemplate("template0"); o.Encoding = 0; return o }(), registered(), "0A000"},
		{"encoding before duplicate", "taken", func() CreateDatabaseOptions { o := valid; o.Encoding = 0; return o }(), registered("taken"), "22023"},
		{"locale before duplicate", "taken", func() CreateDatabaseOptions { o := valid; o.CType = "en_US"; return o }(), registered("taken"), "0A000"},
		{"capability before duplicate", "taken", func() CreateDatabaseOptions { o := valid; o.Capability = "STRATEGY"; return o }(), registered("taken"), "0A000"},
		{"duplicate", "taken", valid, registered("taken"), "42P04"},
	}
	for _, c := range cases {
		err := CheckCreateDatabase(c.name, c.options, c.exists)
		var got *sqlstate.Error
		switch {
		case c.code == "" && err != nil:
			t.Fatal(c.label, err)
		case c.code != "" && (!errors.As(err, &got) || got.Code != c.code):
			t.Fatal(c.label, err)
		}
	}
}

func TestParseCreateDatabaseAdmissionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []parser.DatabaseOption
		allow   bool
		limit   int32
	}{
		{"defaults", nil, true, -1},
		{"disabled", []parser.DatabaseOption{{Name: "allow_connections", Value: "off"}}, false, -1},
		{"one", []parser.DatabaseOption{{Name: "allow_connections", Value: "yes"}, {Name: "connection_limit", Value: "1", Numeric: true}}, true, 1},
		{"zero", []parser.DatabaseOption{{Name: "connection_limit", Value: "0", Numeric: true}}, true, 0},
		{"explicit defaults", []parser.DatabaseOption{{Name: "allow_connections", Default: true}, {Name: "connection_limit", Default: true}}, true, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, err := ParseCreateDatabaseOptions(tc.options)
			if err != nil || options.Capability != "" || options.AllowConnections != tc.allow || options.ConnectionLimit != tc.limit {
				t.Fatal("normalization lost admission policy", options, err)
			}
		})
	}
}
