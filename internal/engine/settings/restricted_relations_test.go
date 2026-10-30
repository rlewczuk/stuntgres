package settings

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRestrictedRelationKindValues(t *testing.T) {
	d := NewRegistry(nil, false).Definitions()["restrict_nonsystem_relation_kind"]
	for _, tc := range []struct {
		text  string
		flags RestrictedRelationKinds
	}{
		{"", 0}, {"   ", 0}, {"view", RestrictView}, {"foreign-table", RestrictForeignTable},
		{"VIEW", RestrictView}, {`"VIEW"`, RestrictView}, {"view,view", RestrictView},
		{"view, foreign-table", RestrictView | RestrictForeignTable},
		{"foreign-table,view", RestrictView | RestrictForeignTable},
	} {
		got, err := d.Parse(tc.text)
		want := Value{Text: tc.text, RestrictedRelations: tc.flags}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got %+v, %v; want %+v", tc.text, got, err, want)
		}
	}
	for _, tc := range []struct{ text, detail string }{
		{"table", `Unrecognized key word: "table".`}, {"all", `Unrecognized key word: "all".`},
		{"bogus", `Unrecognized key word: "bogus".`}, {`""`, `Unrecognized key word: "".`},
		{`"a""b"`, `Unrecognized key word: "a"b".`}, {`"a\b"`, `Unrecognized key word: "a\b".`},
		{",", "List syntax is invalid."}, {"view,", "List syntax is invalid."},
		{"bogus,", "List syntax is invalid."}, {"view,,foreign-table", "List syntax is invalid."},
		{`"unclosed`, "List syntax is invalid."},
	} {
		got, err := d.Parse(tc.text)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "restrict_nonsystem_relation_kind": "` + tc.text + `"`, Detail: tc.detail}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || !reflect.DeepEqual(got, Value{}) {
			t.Fatalf("%q: value %+v, error %+v; want %+v", tc.text, got, diagnostic, want)
		}
	}
}
