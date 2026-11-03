package enginetests_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestGINMaintenanceScalarCandidates(t *testing.T) {
	e, idx, pending := compoundEngine(t, true)
	enginetest.Run(t, e, "CREATE EXTENSION amcheck")
	store := e.Storage.Relations[idx.SQLIndex.Parent]
	if storage.GinPendingIncidences(pending.Gin) == 0 || len(pending.Gin.Pending) != 6 {
		t.Fatal("index-before-INSERT fixture lost genuine scalar pending state")
	}
	original := ginCopyPayload(pending.Gin)
	check := func(root *storage.IndexRoot, extra bool) {
		t.Helper()
		for ordinal, tc := range []struct {
			boundary string
			want     [][]int
		}{
			{"100", [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4}, {4}}},
			{"0", [][]int{{0}, {0, 1}, {1}, {1, 2, 3, 4, 5}, {2, 3, 4, 5}}},
			{"a", [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4}, {4}}},
			{"0.01", [][]int{{0, 1, 2}, {0, 1, 2, 3, 4}, {3, 4}, {3, 4, 5}, {5}}},
		} {
			query := ginInput(t, root.Gin.Columns[ordinal].Binding.Usage.Type, tc.boundary)
			for strategy, positions := range tc.want {
				var want []storage.GinPosting
				for _, position := range positions {
					row := store.Versions[position]
					want = append(want, storage.GinPosting{Row: row.Row, Version: row.Version, Position: position})
				}
				if extra && strategy >= 1 && strategy <= 3 {
					row := store.Versions[7]
					want = append(want, storage.GinPosting{Row: row.Row, Version: row.Version, Position: 7})
				}
				got, err := index.GinScalarCandidates(context.Background(), root.Gin, ordinal, query, strategy+1, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, want) {
					t.Fatal("scalar split strategy changed exact physical occurrences", ordinal, strategy+1, got, want, err)
				}
				got, err = index.GinScalarCandidates(context.Background(), root.Gin, ordinal, sqltype.Null(query.Type()), strategy+1, sqltype.InputEnvironment{})
				if err != nil || got != nil {
					t.Fatal("scalar NULL probe returned candidates", ordinal, strategy+1, got, err)
				}
			}
		}
		before := ginCopyPayload(root.Gin)
		enginetest.Run(t, e, "SELECT gin_index_check('compound_private_i')")
		if e.Storage.IndexRoots[idx.Address()] != root || !reflect.DeepEqual(root.Gin, before) {
			t.Fatal("scalar verification mutated or drained actual split payload")
		}
	}
	check(pending, false)
	enginetest.Run(t, e, "ALTER INDEX compound_private_i SET(fastupdate=off)")
	enginetest.Run(t, e, "INSERT INTO compound_private VALUES(8,'100','0','a','0.01','{2}','cat:1')")
	_, mixed := ginInstalledRoot(t, e, "compound_private_i")
	if len(mixed.Gin.Columns[0].Tokens) == 0 || storage.GinPendingIncidences(mixed.Gin) == 0 {
		t.Fatal("fixture lacks actual scalar main and pending dictionaries")
	}
	check(mixed, true)
	query := ginInput(t, mixed.Gin.Columns[0].Binding.Usage.Type, "100")
	for _, ordinal := range []int{-1, len(mixed.Gin.Columns)} {
		if got, err := index.GinScalarCandidates(context.Background(), mixed.Gin, ordinal, query, 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("invalid root ordinal accepted", ordinal, got, err)
		}
	}
	malformed := *mixed.Gin
	malformed.Pending = malformed.Pending[:1]
	if got, err := index.GinScalarCandidates(context.Background(), &malformed, 0, query, 3, sqltype.InputEnvironment{}); err == nil || got != nil {
		t.Fatal("pending arity accepted", got, err)
	}
	const allowance = 1 << 30
	counted := &spgistInterruptContext{Context: context.Background(), remaining: allowance}
	if _, err := index.GinScalarCandidates(counted, mixed.Gin, 0, query, 4, sqltype.InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	late := &spgistInterruptContext{Context: context.Background(), remaining: allowance - counted.remaining}
	if got, err := index.GinScalarCandidates(late, mixed.Gin, 0, query, 4, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("canceled both-portion union exposed partial/successful candidates", got, err)
	}
	mixedCopy := ginCopyPayload(mixed.Gin)
	enginetest.Run(t, e, "SELECT gin_clean_pending_list('compound_private_i')")
	_, clean := ginInstalledRoot(t, e, "compound_private_i")
	if storage.GinPendingIncidences(clean.Gin) != 0 || len(clean.Entries) != 8 {
		t.Fatal("scalar cleanup changed membership or retained pending")
	}
	check(clean, true)
	if !reflect.DeepEqual(pending.Gin, original) || !reflect.DeepEqual(mixed.Gin, mixedCopy) {
		t.Fatal("cleanup changed retained scalar predecessor")
	}

	// Only authenticated scalar ordinary values and payload-free NULL items are
	// admitted. Array-only placeholders and vector/scalar foreign carriers fail.
	for _, damage := range []string{"null-element", "empty", "foreign-type", "null-ordinary", "lexeme", "null-payload", "null-lexeme"} {
		t.Run("amcheck/"+damage, func(t *testing.T) {
			damaged := *mixed
			damaged.Gin = ginCopyPayload(mixed.Gin)
			column := &damaged.Gin.Pending[0]
			token := &column.Tokens[0].Token
			switch damage {
			case "null-element":
				*token = sqltype.GinToken{Kind: sqltype.GinNullElement}
			case "empty":
				*token = sqltype.GinToken{Kind: sqltype.GinEmptyItem}
			case "foreign-type":
				token.Element = damaged.Gin.Pending[1].Tokens[0].Token.Element
			case "null-ordinary":
				token.Element = sqltype.Null(column.Binding.Usage.Type)
			case "lexeme":
				token.Lexeme = "not-a-scalar"
			case "null-payload":
				column.Tokens[len(column.Tokens)-1].Token.Element = token.Element
			case "null-lexeme":
				column.Tokens[len(column.Tokens)-1].Token.Lexeme = "not-null"
			}
			e.Storage.IndexRoots[idx.Address()] = &damaged
			defer func() { e.Storage.IndexRoots[idx.Address()] = clean }()
			_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "SELECT gin_index_check('compound_private_i')"), nil)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "XX002" || e.Storage.IndexRoots[idx.Address()] != &damaged {
				t.Fatal("malformed scalar token gained verification authority", err)
			}
		})
	}
}

func TestGINMaintenanceScalarRemint(t *testing.T) {
	source, _, _ := compoundEngine(t, true)
	for _, query := range []string{
		"ALTER INDEX compound_private_i SET(fastupdate=off)",
		"INSERT INTO compound_private VALUES(8,'100','0','a','0.01','{2}','cat:1')",
		"ALTER INDEX compound_private_i RESET(fastupdate)",
		"CREATE INDEX compound_empty_i ON compound_private USING gin(e enum_ops) WHERE id<0",
	} {
		enginetest.Run(t, source, query)
	}
	sourceIndex, original := ginInstalledRoot(t, source, "compound_private_i")
	sourcePayload := ginCopyPayload(original.Gin)
	if len(original.Gin.Columns[0].Tokens) == 0 || storage.GinPendingIncidences(original.Gin) == 0 {
		t.Fatal("source lacks genuine installed scalar main and pending storage")
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	for _, same := range []bool{false, true} {
		name := "independent"
		if same {
			name = "same-engine"
		}
		t.Run(name, func(t *testing.T) {
			target := source
			if !same {
				target = engine.New()
			}
			if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			view := target.CatalogRead()
			idx, root := ginInstalledRoot(t, target, "compound_private_i")
			parent := view.MustRelation(idx.SQLIndex.Parent)
			store := target.Storage.Relations[parent.Address()]
			ginAssertOwners(t, root, store)
			if root == original || root.Gin == original.Gin || root.Construction == original.Construction || !index.ConstructionMatches(root, idx) || len(root.Entries) != 8 || storage.GinPendingIncidences(root.Gin) == 0 || len(root.Gin.Columns[0].Tokens) == 0 {
				t.Fatal("remint lost actual split, physical ownership or destination construction")
			}
			for ordinal, key := range idx.SQLIndex.Keys {
				bound, err := routine.BindClassSupport(view, key.Class)
				if err != nil || bound != key.Class {
					t.Fatal("published destination descriptor lacks its actual support capture", ordinal, err)
				}
				for _, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
					if columns[ordinal].Binding.Class != key.Class || columns[ordinal].Binding.Usage.Type != parent.Columns[ordinal+1].Usage.Type {
						t.Fatal("destination descriptor and split payload disagree", ordinal)
					}
				}
				if ordinal < 4 {
					old, current := sourceIndex.SQLIndex.Keys[ordinal].Class, key.Class
					if current.Support(1).Authority == nil || current.Support(1).Authority == old.Support(1).Authority {
						t.Fatal("remint transplanted source installed authority", ordinal)
					}
					if _, err := routine.BindClassSupport(view, old); err == nil {
						t.Fatal("foreign source class became destination authority", ordinal)
					}
				}
			}
			emptyIndex, empty := ginInstalledRoot(t, target, "compound_empty_i")
			bound, err := routine.BindClassSupport(view, emptyIndex.SQLIndex.Keys[0].Class)
			if err != nil || bound != emptyIndex.SQLIndex.Keys[0].Class || bound != empty.Gin.Columns[0].Binding.Class || len(empty.Entries) != 0 || len(empty.Gin.Pending) != 0 || !index.ConstructionMatches(empty, emptyIndex) {
				t.Fatal("empty scalar remint omitted destination descriptor or root capture", err)
			}
			maintenance := &index.GinMaintenance{Context: context.Background(), Index: idx, CommittedIndex: idx, Store: store, View: view, TargetView: view, Roots: target.Storage.IndexRoots, Stores: target.Storage.Relations}
			authority := maintenance.CaptureAuthority(index.GinMaintenanceAuthority{View: view, Index: idx, Parent: parent})
			if err := maintenance.Authorize(authority); err != nil {
				t.Fatal("actual restored owner cannot authenticate its exact descriptor", err)
			}
			before := ginCopyPayload(root.Gin)
			enginetest.Run(t, target, "SELECT gin_clean_pending_list('compound_private_i')")
			current, cleaned := ginInstalledRoot(t, target, "compound_private_i")
			if current != idx || cleaned == root || len(cleaned.Entries) != 8 || storage.GinPendingIncidences(cleaned.Gin) != 0 || !index.ConstructionMatches(cleaned, current) || !reflect.DeepEqual(root.Gin, before) {
				t.Fatal("restored cleanup changed descriptor, membership or retained source payload")
			}
			ginAssertOwners(t, cleaned, store)
		})
	}
	if !reflect.DeepEqual(original.Gin, sourcePayload) {
		t.Fatal("remint or cleanup mutated the retained original split")
	}
}
