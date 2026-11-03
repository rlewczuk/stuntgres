package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBtreeGINTextTokens(t *testing.T) {
	_, _, root := textEngine(t, false)
	for col, tc := range textPrivateCases {
		binding := root.Gin.Columns[col].Binding
		for _, key := range textPrivateKeys(tc) {
			value := ginInput(t, binding.Usage.Type, key.text)
			var tokens []sqltype.GinToken
			if err := binding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || tokens[0].Kind != sqltype.GinOrdinary || !sqltype.SameDatum(tokens[0].Element, value) || tokens[0].Lexeme != "" {
				t.Fatal("empty/zero scalar must be one whole ordinary key", tc.typ, tokens)
			}
		}
		var null []sqltype.GinToken
		if err := binding.Extract(context.Background(), sqltype.Null(binding.Usage.Type), func(token sqltype.GinToken) error { null = append(null, token); return nil }); err != nil || len(null) != 1 || null[0].Kind != sqltype.GinNullItem {
			t.Fatal("NULL category", null, err)
		}
		foreign := *binding.Usage.Type
		for _, v := range []sqltype.Value{sqltype.Null(&foreign), ginInput(t, &foreign, tc.boundary)} {
			if err := binding.Extract(context.Background(), v, func(sqltype.GinToken) error { t.Fatal("foreign generation emitted"); return nil }); err == nil {
				t.Fatal("foreign generation admitted")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := binding.Extract(ctx, sqltype.Null(binding.Usage.Type), func(sqltype.GinToken) error { t.Fatal("canceled emission"); return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	t.Run("selected_bpchar_source", textSelectedBPChar)
	t.Run("name_clipping", func(t *testing.T) {
		binding := root.Gin.Columns[4].Binding
		for _, pair := range [][2]string{{strings.Repeat("x", 63) + "tail", strings.Repeat("x", 63)}, {strings.Repeat("x", 62) + "é", strings.Repeat("x", 62)}} {
			a, b := ginInput(t, binding.Usage.Type, pair[0]), ginInput(t, binding.Usage.Type, pair[1])
			if !sqltype.SameDatum(a, b) {
				t.Fatal("name complete-byte clipping")
			}
		}
	})
}

func TestBtreeGINTextPostings(t *testing.T) {
	_, _, _, store, root := textPhysical(t)
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	for _, entry := range root.Entries {
		for col, value := range entry.Keys {
			if !sqltype.SameDatum(value, store.Versions[entry.Position].Values[col+1]) {
				t.Fatal("dictionary changed source datum")
			}
		}
	}
	for col, tc := range textPrivateCases {
		column := root.Gin.Columns[col]
		keys := textPrivateKeys(tc)
		if len(column.Tokens) != len(keys)+1 {
			t.Fatal("ordinary equivalence keys plus NULL", tc.typ, len(column.Tokens))
		}
		var all, nulls []int
		for row := range 8 {
			if row < len(textPrivateTexts(tc)) {
				all = append(all, row)
			} else {
				nulls = append(nulls, row)
			}
		}
		for ordinal, key := range keys {
			token := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, key.text)}
			cmp, err := column.Binding.Compare(context.Background(), column.Tokens[ordinal].Token, token, sqltype.InputEnvironment{})
			got := ginLookup(t, column, token)
			if err != nil || cmp != 0 || !slices.Equal(got, compoundExactPostings(key.positions)) || !slices.Equal(got, column.Tokens[ordinal].Postings) || &got[0] != &column.Tokens[ordinal].Postings[0] {
				t.Fatal("exact physical equivalence/posting backing", tc.typ, key, got, err)
			}
			var lower, equal, upper []int
			for i, k := range keys {
				switch {
				case i < ordinal:
					lower = append(lower, k.positions...)
				case i == ordinal:
					equal = append(equal, k.positions...)
				default:
					upper = append(upper, k.positions...)
				}
			}
			wants := [][]int{lower, append(slices.Clone(lower), equal...), equal, append(slices.Clone(equal), upper...), upper}
			for i := range wants {
				slices.Sort(wants[i])
			}
			column.NonNull = []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}
			networkProbeProof(t, column, key.text, tc.absent, wants, all)
		}
		if !slices.Equal(ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}), compoundExactPostings(nulls)) {
			t.Fatal("NULL exact triples", tc.typ)
		}
		foreign := *column.Binding.Usage.Type
		if got, err := index.GinScalarCandidates(context.Background(), root.Gin, col, sqltype.Null(&foreign), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("foreign query generation admitted", got, err)
		}
	}
	ginAssertOwners(t, root, store)
	if !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
		t.Fatal("probe mutated predecessor")
	}
}

func textSelectedBPChar(t *testing.T) {
	e, _, _ := textEngine(t, false)
	enginetest.Run(t, e, "CREATE TABLE text_selected(t text,v varchar)")
	enginetest.Run(t, e, "CREATE INDEX text_selected_i ON text_selected USING gin(t bpchar_ops,v bpchar_ops)")
	idx, _ := ginInstalledRoot(t, e, "text_selected_i")
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	store := &storage.Relation{}
	for row, text := range []string{"ab", "ab ", "ab  ", "ab\t", "ab\n", ""} {
		var values []sqltype.Value
		for _, c := range parent.Columns {
			values = append(values, ginInput(t, c.Usage.Type, text))
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + row), Version: ids.VersionID(201 + row), Born: 1, Values: values})
	}
	root := networkBuild(t, view, parent, idx, store)
	for col, column := range root.Gin.Columns {
		if len(column.Tokens) != 4 {
			t.Fatal("selected bpchar must collapse only trailing ASCII spaces", len(column.Tokens))
		}
		for _, query := range []string{"ab", "ab ", "ab  "} {
			got, err := index.GinScalarCandidates(context.Background(), root.Gin, col, ginInput(t, column.Binding.Usage.Type, query), 3, sqltype.InputEnvironment{})
			if err != nil || !slices.Equal(got, compoundExactPostings([]int{0, 1, 2})) {
				t.Fatal("selected bpchar equivalence", col, query, got, err)
			}
		}
		for _, entry := range root.Entries {
			if !sqltype.SameDatum(entry.Keys[col], store.Versions[entry.Position].Values[col]) {
				t.Fatal("selected comparison mutated source")
			}
		}
		ref, err := sqltype.RegisteredCollation(uint32(90000+col), 6411)
		if err != nil {
			t.Fatal(err)
		}
		usage := column.Binding.Usage
		usage.Collation = ref
		bound, err := sqltype.BindGinTokens(usage, column.Binding.Class)
		if err != nil || bound.Usage != usage || bound.Same(column.Binding) {
			t.Fatal("actual collation identity not captured", err)
		}
		a, b := sqltype.GinToken{Element: ginInput(t, usage.Type, "ab")}, sqltype.GinToken{Element: ginInput(t, usage.Type, "ab ")}
		if cmp, err := bound.Compare(context.Background(), a, b, sqltype.InputEnvironment{}); err != nil || cmp != 0 {
			t.Fatal("selected bpchar registered collation", cmp, err)
		}
		invalid := usage
		invalid.Collation = sqltype.CollationRef{ID: ref.ID, Derivation: sqltype.Default}
		if _, err := sqltype.BindGinTokens(invalid, column.Binding.Class); err == nil {
			t.Fatal("forged collation authority accepted")
		}
		domain, err := sqltype.Domain(sqltype.ID(200000+col), "text_selected_domain", "TEXT_SELECTED_DOMAIN", usage)
		if err != nil {
			t.Fatal(err)
		}
		domainBinding, err := sqltype.BindGinTokens(domain.Usage(), column.Binding.Class)
		if err != nil {
			t.Fatal(err)
		}
		value := ginInput(t, domain, "ab ")
		var tokens []sqltype.GinToken
		if err := domainBinding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil || len(tokens) != 1 || !sqltype.SameDatum(tokens[0].Element, value) || domainBinding.Usage.Collation != ref {
			t.Fatal("domain source generation/collation changed", tokens, err)
		}
	}
	ginAssertOwners(t, root, store)
}
