package parser

import (
	"slices"
	"testing"
)

func TestFormatEncodingNamesAST(t *testing.T) {
	for _, tc := range []struct {
		query               string
		local, defaultValue bool
		values              []string
	}{
		{"SET NAMES 'utf-8'", false, false, []string{"utf-8"}},
		{"SET SESSION NAMES UNICODE", false, false, []string{"unicode"}},
		{"SET LOCAL NAMES 'UNICODE'", true, false, []string{"UNICODE"}},
		{"SET NAMES DEFAULT", false, true, nil},
		{"SET SESSION NAMES DEFAULT", false, true, nil},
		{"SET LOCAL NAMES DEFAULT", true, true, nil},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		set, ok := statements[0].(*Set)
		if !ok || set.Name.Text != "client_encoding" || set.Local != tc.local || set.Default != tc.defaultValue || !slices.Equal(set.Values, tc.values) || set.Interval != nil {
			t.Fatal(tc.query, statements[0])
		}
		ordinary := "SET "
		if tc.local {
			ordinary += "LOCAL "
		}
		ordinary += "client_encoding TO "
		if tc.defaultValue {
			ordinary += "DEFAULT"
		} else {
			ordinary += "'" + tc.values[0] + "'"
		}
		other, err := Parse(ordinary)
		if err != nil {
			t.Fatal(ordinary, err)
		}
		otherSet := other[0].(*Set)
		if set.Name.Text != otherSet.Name.Text || set.Local != otherSet.Local || set.Default != otherSet.Default || !slices.Equal(set.Values, otherSet.Values) {
			t.Fatal("different assignment AST", set, otherSet)
		}
	}
	for _, query := range []string{"CREATE TABLE names(names text)", "SELECT names AS names FROM names", "SET names TO 'x'", "SHOW names", "CREATE TABLE sample(v names)"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("names lost its unreserved grammar role", query, err)
		}
	}
}
