package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestDependencyIdentifierQuotesLikeQuoteIdentifier(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"_x1", "_x1"},
		{"abort", "abort"}, // unreserved keyword
		{"select", `"select"`},
		{"int", `"int"`},
		{"xmltable", `"xmltable"`},
		{"Mixed", `"Mixed"`},
		{"1st", `"1st"`},
		{"has space", `"has space"`},
		{"", `""`},
		{`a"b`, `"a""b"`},
	} {
		if got := DependencyIdentifier(tc.in); got != tc.want {
			t.Errorf("DependencyIdentifier(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	if got := QuoteIdentifier(`plain"x`); got != `"plain""x"` {
		t.Errorf("QuoteIdentifier = %s", got)
	}
}

func TestTypeDiagnosticNameAtQualifiesHiddenTypes(t *testing.T) {
	s1 := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}, ObjectName: "s1"}
	odd := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 901}, ObjectName: "Odd"}
	mood := &Type{SQLType: sqltype.LookupOnly(910, "mood", 911), ObjectNamespace: s1.Address()}
	moods := &Type{SQLType: sqltype.LookupArray(911, "_mood", mood.SQLType), ObjectNamespace: s1.Address()}
	shadow := &Type{SQLType: sqltype.LookupOnly(912, "mood", 0), ObjectNamespace: odd.Address()}
	state := &State{epoch: 1, descriptors: map[ids.ObjectAddress]Descriptor{}, names: map[ids.NameKey]ids.ObjectAddress{}}
	for _, d := range []Descriptor{s1, odd, mood, moods, shadow} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(DescriptorNameKey(d), d.Address())
	}
	view := Read{state: state}
	path := func(ns ...*Namespace) []ids.ObjectAddress {
		out := []ids.ObjectAddress{}
		for _, n := range ns {
			out = append(out, n.Address())
		}
		return out
	}
	for _, tc := range []struct {
		name string
		typ  *Type
		path []ids.ObjectAddress
		want string
	}{
		{"nil path", mood, nil, "mood"},
		{"first on path", mood, path(s1), "mood"},
		{"empty path", mood, path(), "s1.mood"},
		{"shadowed", mood, path(odd, s1), "s1.mood"},
		{"quoted schema", shadow, path(s1), `"Odd".mood`},
		{"array visible", moods, path(s1), "mood[]"},
		{"array shadowed", moods, path(odd, s1), "s1.mood[]"},
	} {
		if got := TypeDiagnosticNameAt(view, tc.typ, tc.path); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
