package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestDictIntOptions(t *testing.T) {
	e, _, ext := dictIntUnitInstalled(t)
	view := e.CatalogRead()
	template, _ := view.TSTemplate(ext.Members[2])
	option := func(name, value string, numeric bool) catalog.DictionaryOption {
		return catalog.DictionaryOption{Name: name, Value: value, Supplied: true, Numeric: numeric}
	}
	for _, tc := range []struct {
		options            []catalog.DictionaryOption
		maxlen             int
		rejectlong, absval bool
	}{
		{nil, 6, false, false},
		{[]catalog.DictionaryOption{option("maxlen", "6.7", true)}, 6, false, false},
		{[]catalog.DictionaryOption{option("maxlen", " \t\n\r\v\f+3tail", false)}, 3, false, false},
		{[]catalog.DictionaryOption{option("maxlen", "03rest", false)}, 3, false, false},
		{[]catalog.DictionaryOption{option("maxlen", "2", true), option("maxlen", "3", true), option("absval", "false", false), option("absval", "ON", false), option("rejectlong", "true", false), option("rejectlong", "off", false)}, 3, false, true},
		{[]catalog.DictionaryOption{option("rejectlong", "1", true), option("absval", "0", true)}, 6, true, false},
		{[]catalog.DictionaryOption{{Name: "rejectlong"}, {Name: "absval"}}, 6, true, true},
	} {
		original := slices.Clone(tc.options)
		runtime, err := extension.CompileDictionary(t.Context(), view, template, tc.options, e.TextSearchFiles)
		if err != nil || !slices.Equal(tc.options, original) {
			t.Fatal("immutable ordered options", runtime, err)
		}
		d, ok := runtime.(*functions.IntDictionary)
		if !ok || !d.Configured(tc.maxlen, tc.rejectlong, tc.absval) {
			t.Fatal("effective option settings", tc, runtime)
		}
	}
	for _, tc := range []struct {
		options       []catalog.DictionaryOption
		code, message string
	}{
		{[]catalog.DictionaryOption{option("maxlen", "0", true)}, "22023", "maxlen value has to be >= 1"},
		{[]catalog.DictionaryOption{option("maxlen", "-1", true)}, "22023", "maxlen value has to be >= 1"},
		{[]catalog.DictionaryOption{option("maxlen", "abc", false)}, "22023", "maxlen value has to be >= 1"},
		{[]catalog.DictionaryOption{option("maxlen", "", false)}, "22023", "maxlen value has to be >= 1"},
		{[]catalog.DictionaryOption{{Name: "maxlen"}}, "42601", "maxlen requires a parameter"},
		{[]catalog.DictionaryOption{option("MAXLEN", "3", true)}, "22023", `unrecognized intdict parameter: "MAXLEN"`},
		{[]catalog.DictionaryOption{option("maxlen", "2", true), option("unknown", "1", true)}, "22023", `unrecognized intdict parameter: "unknown"`},
		{[]catalog.DictionaryOption{option("rejectlong", "1", false)}, "42601", "rejectlong requires a Boolean value"},
		{[]catalog.DictionaryOption{option("rejectlong", "2", true)}, "42601", "rejectlong requires a Boolean value"},
		{[]catalog.DictionaryOption{option("rejectlong", "yes", false)}, "42601", "rejectlong requires a Boolean value"},
		{[]catalog.DictionaryOption{option("absval", "f", false)}, "42601", "absval requires a Boolean value"},
	} {
		runtime, err := extension.CompileDictionary(t.Context(), view, template, tc.options, e.TextSearchFiles)
		if runtime != nil {
			t.Fatal("invalid options published a runtime", tc, runtime)
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message})
	}
	original := view.MustTSDictionary(ext.Members[3])
	before := e.CatalogRead().State()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if runtime, err := extension.CompileDictionary(ctx, view, template, nil, e.TextSearchFiles); runtime != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled compiler published a runtime", runtime, err)
	}
	if e.CatalogRead().State() != before || e.CatalogRead().MustTSDictionary(original.Address()) != original {
		t.Fatal("option compilation mutated catalog")
	}
	old := []catalog.DictionaryOption{option("maxlen", "3", true), option("rejectlong", "true", false), option("absval", "true", false)}
	copy := slices.Clone(old)
	reset := extension.MergeDictionaryOptions(old, []catalog.DictionaryOption{{Name: "maxlen"}, {Name: "rejectlong"}, {Name: "absval"}})
	runtime, err := extension.CompileDictionary(t.Context(), view, template, reset, e.TextSearchFiles)
	if err != nil || !runtime.(*functions.IntDictionary).Configured(6, false, false) || len(reset) != 0 || !slices.Equal(old, copy) {
		t.Fatal("reset altered source settings or missed defaults", runtime, err)
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, original); err != nil {
		t.Fatal(err)
	}
}
