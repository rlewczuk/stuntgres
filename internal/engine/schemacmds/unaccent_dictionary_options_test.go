package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func TestUnaccentDictionaryOptionsOrder(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	template, ok := view.TSTemplate(ext.Members[4])
	if !ok {
		t.Fatal("installed unaccent template missing")
	}
	rule := func(value string) catalog.DictionaryOption {
		return catalog.DictionaryOption{Name: "rules", Value: value, Supplied: true}
	}
	good, missing := rule("unaccent"), rule("unaccent_missing")
	bogus := catalog.DictionaryOption{Name: "bogus", Value: "1", Supplied: true}
	for _, tc := range []struct {
		name          string
		options       []catalog.DictionaryOption
		code, message string
	}{
		{"missing_rules", nil, "22023", "missing Rules parameter"},
		{"resource_before_unknown", []catalog.DictionaryOption{missing, bogus}, "F0000", `could not open unaccent file "unaccent_missing.rules": No such file or directory`},
		{"unknown_before_resource", []catalog.DictionaryOption{bogus, missing}, "22023", `unrecognized Unaccent parameter: "bogus"`},
		{"resource_before_duplicate", []catalog.DictionaryOption{missing, good}, "F0000", `could not open unaccent file "unaccent_missing.rules": No such file or directory`},
		{"loaded_before_duplicate", []catalog.DictionaryOption{good, missing}, "22023", "multiple Rules parameters"},
		{"loaded_before_unknown", []catalog.DictionaryOption{good, bogus}, "22023", `unrecognized Unaccent parameter: "bogus"`},
		{"loaded_before_duplicate_valid", []catalog.DictionaryOption{good, good}, "22023", "multiple Rules parameters"},
		{"valid", []catalog.DictionaryOption{good}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.options)
			runtime, err := extension.CompileDictionary(context.Background(), view, template, tc.options, nil)
			if tc.code != "" {
				var diagnostic *sqlstate.Error
				if runtime != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
					t.Fatal("ordered initialization", runtime, err)
				}
			} else if err != nil || runtime != functions.StockUnaccent {
				t.Fatal("genuine stock runtime", runtime, err)
			}
			if !reflect.DeepEqual(tc.options, before) || e.CatalogRead().State() != view.State() {
				t.Fatal("detached compile mutated inputs/catalog")
			}
		})
	}
	old := []catalog.DictionaryOption{good}
	for _, tc := range []struct {
		name          string
		changes, want []catalog.DictionaryOption
		code          string
	}{
		{"append_rules_first", []catalog.DictionaryOption{missing, bogus}, []catalog.DictionaryOption{missing, bogus}, "F0000"},
		{"append_unknown_first", []catalog.DictionaryOption{bogus, missing}, []catalog.DictionaryOption{bogus, missing}, "22023"},
		{"last_valid_replacement", []catalog.DictionaryOption{missing, good}, []catalog.DictionaryOption{good}, ""},
		{"last_missing_replacement", []catalog.DictionaryOption{good, missing}, []catalog.DictionaryOption{missing}, "F0000"},
		{"remove_rules", []catalog.DictionaryOption{{Name: "rules"}}, []catalog.DictionaryOption{}, "22023"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.changes)
			merged := extension.MergeDictionaryOptions(old, tc.changes)
			if !slices.Equal(merged, tc.want) || !slices.Equal(old, []catalog.DictionaryOption{good}) || !slices.Equal(tc.changes, before) {
				t.Fatal("remove-then-append merge ownership", merged)
			}
			runtime, err := extension.CompileDictionary(context.Background(), view, template, merged, nil)
			if tc.code != "" {
				var d *sqlstate.Error
				if runtime != nil || !errors.As(err, &d) || d.Code != tc.code {
					t.Fatal("merged init precedence", runtime, err)
				}
			} else if err != nil || runtime != functions.StockUnaccent {
				t.Fatal(runtime, err)
			}
			if len(merged) != 0 {
				merged[0].Value = "changed"
			}
			if old[0] != good || !slices.Equal(tc.changes, before) {
				t.Fatal("merged options retain caller backing")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if runtime, err := extension.CompileDictionary(ctx, view, template, []catalog.DictionaryOption{good}, nil); runtime != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary canceled initialization", runtime, err)
	}
	if current := view.MustTSDictionary(ext.Members[5]); current.Runtime != functions.StockUnaccent || !slices.Equal(current.Options, old) {
		t.Fatal("failed initialization replaced installed dictionary")
	}
}
