package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestXsynDictionaryOptionOrder(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dict_xsyn")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dict_xsyn"}))
	template, _ := view.TSTemplate(ext.Members[2])
	option := func(name, value string, numeric bool) catalog.DictionaryOption {
		return catalog.DictionaryOption{Name: name, Value: value, Supplied: true, Numeric: numeric}
	}
	for _, tc := range []struct {
		options    []catalog.DictionaryOption
		present    bool
		name       string
		m, s, k, y bool
	}{
		{nil, false, "", true, false, true, true},
		{[]catalog.DictionaryOption{option("rules", "xsyn_sample", false)}, true, "xsyn_sample", true, false, true, true},
		{[]catalog.DictionaryOption{option("rules", "../bad", false), option("rules", "xsyn_sample", false)}, true, "xsyn_sample", true, false, true, true},
		{[]catalog.DictionaryOption{option("rules", "xsyn_missing", false), option("rules", "unaccent", false)}, true, "unaccent", true, false, true, true},
		{[]catalog.DictionaryOption{option("matchorig", "false", false), option("matchorig", "ON", false), option("matchsynonyms", "1", true), option("keeporig", "0", true), option("keepsynonyms", "OFF", false)}, false, "", true, true, false, false},
		{[]catalog.DictionaryOption{{Name: "matchorig"}, {Name: "matchsynonyms"}, {Name: "keeporig"}, {Name: "keepsynonyms"}}, false, "", true, true, true, true},
	} {
		before := slices.Clone(tc.options)
		runtime, err := extension.CompileDictionary(t.Context(), view, template, tc.options, e.TextSearchFiles)
		d, ok := runtime.(*functions.XsynDictionary)
		if err != nil || !ok || !d.Configured(tc.present, tc.name, tc.m, tc.s, tc.k, tc.y) || !slices.Equal(tc.options, before) {
			t.Fatal("ordered immutable effective options", tc, runtime, err)
		}
	}
	for _, name := range []string{"matchorig", "matchsynonyms", "keeporig", "keepsynonyms"} {
		for _, value := range []struct {
			text              string
			numeric, accepted bool
		}{
			{"0", true, true}, {"1", true, true}, {"TrUe", false, true}, {"FaLsE", false, true}, {"ON", false, true}, {"off", false, true},
			{"0", false, false}, {"1", false, false}, {"yes", false, false}, {"no", false, false}, {"t", false, false}, {"f", false, false}, {"2", true, false}, {"-1", true, false},
		} {
			runtime, err := extension.CompileDictionary(t.Context(), view, template, []catalog.DictionaryOption{option(name, value.text, value.numeric)}, e.TextSearchFiles)
			if value.accepted {
				if err != nil || runtime == nil {
					t.Fatal(name, value, runtime, err)
				}
			} else {
				if runtime != nil {
					t.Fatal("invalid Boolean published a runtime", runtime)
				}
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42601", Message: name + " requires a Boolean value"})
			}
		}
	}
	for _, tc := range []struct {
		options       []catalog.DictionaryOption
		code, message string
	}{
		{[]catalog.DictionaryOption{{Name: "rules"}}, "42601", "rules requires a parameter"},
		{[]catalog.DictionaryOption{option("MATCHORIG", "true", false)}, "22023", `unrecognized xsyn parameter: "MATCHORIG"`},
		{[]catalog.DictionaryOption{option("rules", "../bad", false), option("unknown", "1", true)}, "22023", `unrecognized xsyn parameter: "unknown"`},
		{[]catalog.DictionaryOption{option("matchorig", "yes", false), option("unknown", "1", true)}, "42601", "matchorig requires a Boolean value"},
		{[]catalog.DictionaryOption{option("unknown", "1", true), option("matchorig", "yes", false)}, "22023", `unrecognized xsyn parameter: "unknown"`},
		{[]catalog.DictionaryOption{option("rules", "xsyn_sample", false), option("rules", "xsyn_missing", false)}, "F0000", `could not open synonym file "xsyn_missing.rules": No such file or directory`},
		{[]catalog.DictionaryOption{{Name: "keeporig"}, option("rules", "xsyn_missing", false)}, "F0000", `could not open synonym file "xsyn_missing.rules": No such file or directory`},
		{[]catalog.DictionaryOption{option("rules", "", false)}, "F0000", `could not open synonym file ".rules": No such file or directory`},
	} {
		runtime, err := extension.CompileDictionary(t.Context(), view, template, tc.options, e.TextSearchFiles)
		if runtime != nil {
			t.Fatal("invalid options published runtime", runtime)
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message})
	}
	old := []catalog.DictionaryOption{option("rules", "xsyn_sample", false), option("matchorig", "false", false), option("matchsynonyms", "true", false), option("keeporig", "false", false), option("keepsynonyms", "false", false)}
	copy := slices.Clone(old)
	reset := extension.MergeDictionaryOptions(old, []catalog.DictionaryOption{{Name: "rules"}, {Name: "matchorig"}, {Name: "matchsynonyms"}, {Name: "keeporig"}, {Name: "keepsynonyms"}})
	runtime, err := extension.CompileDictionary(t.Context(), view, template, reset, e.TextSearchFiles)
	if err != nil || !runtime.(*functions.XsynDictionary).Configured(false, "", true, false, true, true) || len(reset) != 0 || !slices.Equal(copy, old) {
		t.Fatal("bare ALTER removal changed source or missed defaults", runtime, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if runtime, err := extension.CompileDictionary(ctx, view, template, nil, e.TextSearchFiles); runtime != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled compilation", runtime, err)
	}
	if e.CatalogRead().State() != view.State() || e.CatalogRead().MustTSDictionary(ext.Members[3]) != view.MustTSDictionary(ext.Members[3]) {
		t.Fatal("detached option compilation changed catalog")
	}
}
