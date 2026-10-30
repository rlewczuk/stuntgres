package settings

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCreateroleSelfGrantParsing(t *testing.T) {
	d := NewRegistry(nil, false).Definitions()["createrole_self_grant"]
	for _, tc := range []struct {
		text string
		want SelfGrantOptions
	}{
		{"", 0}, {"   ", 0}, {"SET", SelfGrantSet}, {"inherit", SelfGrantInherit},
		{"inherit,set", SelfGrantSet | SelfGrantInherit}, {"  SET ,inherit ", SelfGrantSet | SelfGrantInherit},
		{"SET,SET", SelfGrantSet}, {`"SeT"`, SelfGrantSet}, {`"INHERIT", "set"`, SelfGrantSet | SelfGrantInherit},
	} {
		t.Run(tc.text, func(t *testing.T) {
			want := Value{Text: tc.text, SelfGrant: tc.want}
			got, err := ParseCreateroleSelfGrant(tc.text)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, err, want)
			}
			parsed, err := d.Parse(tc.text)
			if err != nil || !reflect.DeepEqual(parsed, want) {
				t.Fatal("definition parse", parsed, err)
			}
		})
	}
	for _, tc := range []struct{ text, detail string }{
		{"bogus", `Unrecognized key word: "bogus".`}, {`""`, `Unrecognized key word: "".`},
		{"SET,", "List syntax is invalid."}, {",SET", "List syntax is invalid."},
		{"SET,,INHERIT", "List syntax is invalid."}, {`"set`, "List syntax is invalid."},
		{"SET INHERIT", "List syntax is invalid."}, {"bogus,SET,", "List syntax is invalid."},
	} {
		_, err := d.Parse(tc.text)
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "createrole_self_grant": "%s"`, tc.text), Detail: tc.detail}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatalf("%q: got %#v want %#v", tc.text, err, want)
		}
	}
	if d.DefaultValue().SelfGrant != 0 || d.DefaultValue().Text != "" {
		t.Fatal("nonempty boot default")
	}
}
