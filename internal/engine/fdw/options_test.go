package fdw

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestFDWOptionsTransform(t *testing.T) {
	old := []catalog.ForeignOption{{"a", "1"}, {"b", "2"}}
	change := func(action, name, value string) parser.ForeignOption {
		return parser.ForeignOption{Action: action, Name: parser.Identifier{Text: name}, Value: value}
	}
	got, err := TransformOptions(old, []parser.ForeignOption{change("set", "a", "3"), change("drop", "b", ""), change("add", "c", "4")})
	if err != nil || !reflect.DeepEqual(got, []catalog.ForeignOption{{"a", "3"}, {"c", "4"}}) || !reflect.DeepEqual(old, []catalog.ForeignOption{{"a", "1"}, {"b", "2"}}) {
		t.Fatal(got, old, err)
	}
	for _, tc := range []struct {
		changes []parser.ForeignOption
		code    string
	}{
		{[]parser.ForeignOption{change("set", "a", "bad"), change("add", "b", "duplicate")}, "42710"},
		{[]parser.ForeignOption{change("drop", "absent", "")}, "42704"},
		{[]parser.ForeignOption{change("drop", "x=y", "")}, "42704"},
		{[]parser.ForeignOption{change("add", "x=y", "v"), change("add", "b", "duplicate")}, "42710"},
		{[]parser.ForeignOption{change("add", "x=y", "v")}, "22023"},
	} {
		got, err = TransformOptions(old, tc.changes)
		var diagnostic *sqlstate.Error
		if got != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || old[0].Value != "1" {
			t.Fatal(got, err, old)
		}
	}
	got, err = TransformOptions(nil, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("empty validator list", got, err)
	}
	got, err = TransformOptions(nil, []parser.ForeignOption{change("add", "x", "1"), change("set", "x", "2"), change("drop", "x", ""), change("add", "x", "3")})
	if err != nil || !reflect.DeepEqual(got, []catalog.ForeignOption{{"x", "3"}}) {
		t.Fatal(got, err)
	}
	got, err = TransformOptions(old, []parser.ForeignOption{change("add", "x=y", "v"), change("drop", "x=y", ""), change("set", "a", "3"), change("set", "a", "4"), change("drop", "a", ""), change("add", "a", "")})
	if err != nil || !reflect.DeepEqual(got, []catalog.ForeignOption{{"b", "2"}, {"a", ""}}) || !reflect.DeepEqual(old, []catalog.ForeignOption{{"a", "1"}, {"b", "2"}}) {
		t.Fatal("ordered surviving options", got, old, err)
	}
}
