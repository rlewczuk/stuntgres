package settings

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestParseSearchPath(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{{`"dot.name", "comma,name", "quote""name"`, []string{"dot.name", "comma,name", "quote\"name"}}, {`"$user", public`, []string{"$user", "public"}}, {`""`, []string{""}}, {`"", s2`, []string{"", "s2"}}, {"", nil}, {`a"b`, []string{`a"b`}}} {
		got, err := parseSearchPath(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got, err)
		}
	}
	for _, input := range []string{",", "s1,", `"unterminated`, `a b`} {
		_, err := parseSearchPath(input)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22023" {
			t.Fatal(input, err)
		}
	}
}
