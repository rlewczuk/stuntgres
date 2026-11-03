package core_test

import (
	"bytes"
	"context"
	"maps"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func ginAmcheckUnitRoot(t *testing.T) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	return ginAmcheckUnitPortion(t, false)
}
func ginAmcheckUnitPortion(t *testing.T, pending bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	amcheckUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE t(n integer,a integer[],v tsvector)")
	if pending {
		enginetest.Run(t, e, "CREATE INDEX i ON t USING gin(a,v)")
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'{1,1,2}','cat cat dog'),(2,'{1,1,2}','cat'),(3,'{NULL}',''),(4,'{}',NULL),(5,NULL,NULL),(6,'[4:5]={2,3}','dog')")
	if !pending {
		enginetest.Run(t, e, "CREATE INDEX i ON t USING gin(a,v)")
	}
	d, r := btreeRoot(t, e, "i")
	if (storage.GinPendingIncidences(r.Gin) != 0) != pending {
		t.Fatal("fixture did not produce requested physical portion")
	}
	return e, d, r
}
func ginAmcheckUnitExpression(t *testing.T, e *core.Engine) plan.InstalledAmcheckExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "gin_index_check"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "i"}}}}
	n, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return n.(plan.InstalledAmcheckExpr)
}
func ginAmcheckClone(r *storage.IndexRoot) *storage.IndexRoot {
	c := *r
	c.Entries = append([]storage.IndexEntry(nil), r.Entries...)
	for i := range c.Entries {
		c.Entries[i].Keys = append([]sqltype.Value(nil), r.Entries[i].Keys...)
	}
	c.Membership, c.RowEntries = maps.Clone(r.Membership), maps.Clone(r.RowEntries)
	if r.Gin != nil {
		c.Gin = &storage.GinRoot{Columns: ginAmcheckCloneColumns(r.Gin.Columns), Pending: ginAmcheckCloneColumns(r.Gin.Pending)}
	}
	return &c
}
func ginAmcheckCloneColumns(columns []storage.GinColumn) []storage.GinColumn {
	copied := append([]storage.GinColumn(nil), columns...)
	for i := range copied {
		col := &copied[i]
		col.NonNull = append([]storage.GinPosting(nil), col.NonNull...)
		col.Tokens = append([]storage.GinPostingList(nil), col.Tokens...)
		for j := range col.Tokens {
			col.Tokens[j].Postings = append([]storage.GinPosting(nil), col.Tokens[j].Postings...)
		}
	}
	return copied
}
func TestAmcheckGINTypedPostings(t *testing.T) {
	e, d, r := ginAmcheckUnitRoot(t)
	owners := map[int64]storage.GinPosting{}
	for _, entry := range r.Entries {
		row := e.Storage.Relations[d.SQLIndex.Parent].Versions[entry.Position]
		n, err := row.Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		owners[n] = storage.GinPosting{Row: entry.Row, Version: entry.Version, Position: entry.Position}
	}
	for ordinal, wants := range []map[string][]int64{
		{"1": {1, 2}, "2": {1, 2, 6}, "3": {6}, "null_element": {3}, "empty": {4}, "null": {5}},
		{"cat": {1, 2}, "dog": {1, 6}, "empty": {3}, "null": {4, 5}},
	} {
		col := r.Gin.Columns[ordinal]
		if len(col.Tokens) != len(wants) {
			t.Fatal("dictionary count", ordinal, col)
		}
		for _, list := range col.Tokens {
			name := list.Token.Lexeme
			switch list.Token.Kind {
			case sqltype.GinNullElement:
				name = "null_element"
			case sqltype.GinEmptyItem:
				name = "empty"
			case sqltype.GinNullItem:
				name = "null"
			case sqltype.GinOrdinary:
				if ordinal == 0 {
					n, err := list.Token.Element.Integer()
					if err != nil {
						t.Fatal(err)
					}
					name = strconv.FormatInt(n, 10)
				}
			}
			ids, ok := wants[name]
			if !ok {
				t.Fatal("unexpected token", ordinal, name)
			}
			if len(list.Postings) != len(ids) {
				t.Fatal("posting occurrences", name, list.Postings)
			}
			for i, n := range ids {
				if list.Postings[i] != owners[n] {
					t.Fatal("exact typed owner", name, list.Postings, owners)
				}
			}
		}
		nonNull := []int64{1, 2, 3, 4, 6}
		if ordinal == 1 {
			nonNull = []int64{1, 2, 3, 6}
		}
		if len(col.NonNull) != len(nonNull) {
			t.Fatal("nonnull", col.NonNull)
		}
		for i, n := range nonNull {
			if col.NonNull[i] != owners[n] {
				t.Fatal("nonnull owner", col.NonNull)
			}
		}
	}
	before := ginAmcheckClone(r)
	if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, before) {
		t.Fatal("checker mutated retained dictionaries")
	}
	t.Run("live_presentation", func(t *testing.T) {
		for _, q := range []string{"ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_array", "ALTER OPERATOR FAMILY pg_catalog.array_ops USING gin RENAME TO gin_array_family"} {
			previousIndex, previous := btreeRoot(t, e, "i")
			enginetest.Run(t, e, q)
			currentIndex, current := btreeRoot(t, e, "i")
			if previousIndex == currentIndex || previous == current || &previous.Gin.Columns[0].Tokens[0] != &current.Gin.Columns[0].Tokens[0] {
				t.Fatal("presentation refresh lost immutable dictionary backing")
			}
			if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
				t.Fatal("current class/family authority", err)
			}
		}
	})
	t.Run("control_generations", func(t *testing.T) {
		e := engine.New()
		amcheckUnitInstalled(t, e)
		for _, q := range []string{"CREATE DOMAIN gin_elem AS integer", "CREATE TABLE t(a gin_elem[])", "INSERT INTO t VALUES('{1,1,2}'),('{NULL}'),('{}'),(NULL)", "CREATE INDEX i ON t USING gin(a)"} {
			enginetest.Run(t, e, q)
		}
		oldIndex, oldRoot := btreeRoot(t, e, "i")
		oldExpression := ginAmcheckUnitExpression(t, e)
		oldType := oldRoot.Entries[0].Keys[0].Type()
		oldCopy := ginAmcheckClone(oldRoot)
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), e, &dump); err != nil {
			t.Fatal(err)
		}
		keeper := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, keeper, "CREATE TEMP TABLE gin_keep(n integer)")
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		currentIndex, current := btreeRoot(t, e, "i")
		fresh := ginAmcheckUnitExpression(t, e)
		if currentIndex == oldIndex || currentIndex.Address() == oldIndex.Address() || current == oldRoot || current.Entries[0].Keys[0].Type() == oldType || fresh.Procedure == oldExpression.Procedure || fresh.Procedure.Origin.Declaration != 8 {
			t.Fatal("control replay did not remint exact GIN owners")
		}
		if err := stored.ValidateInstalledAmcheck(oldExpression, e.CatalogRead()); err == nil {
			t.Fatal("old captured expression gained replacement authority")
		}
		if err := stored.ValidateInstalledAmcheck(fresh, e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		store := e.Storage.Relations[currentIndex.SQLIndex.Parent]
		for _, entry := range current.Entries {
			row := store.Versions[entry.Position]
			if entry.Row != row.Row || entry.Version != row.Version || entry.Keys[0].Type() != currentIndex.Columns[0].Usage.Type {
				t.Fatal("replacement retained source row/type authority")
			}
		}
		if !reflect.DeepEqual(oldRoot, oldCopy) {
			t.Fatal("replacement mutated retained source dictionary")
		}
		if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAmcheckGINLogicalCorruption(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "main"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			for _, damage := range []string{"missing", "spurious", "wrong_token", "duplicate_posting", "reversed_posting", "row", "version", "position", "empty_list", "swapped_token", "duplicate_token", "category", "placeholder_payload", "placeholder_lexeme", "null_ordinary", "vector_element", "vector_empty_lexeme", "vector_null_element", "element_generation", "binding", "columns", "arity", "key_generation", "entry_owner", "duplicate_entry", "membership", "row_entries", "missing_nonnull", "spurious_nonnull", "reversed_nonnull", "missing_payload", "mixed_payload", "overlapping_portions", "cross_column_placement"} {
				t.Run(damage, func(t *testing.T) {
					e, d, r := ginAmcheckUnitPortion(t, pending)
					before := ginAmcheckClone(r)
					next := ginAmcheckClone(r)
					columns := next.Gin.Columns
					if pending {
						columns = next.Gin.Pending
					}
					col := &columns[0]
					switch damage {
					case "missing":
						col.Tokens[0].Postings = col.Tokens[0].Postings[1:]
					case "spurious":
						col.Tokens[0].Postings = append(col.Tokens[0].Postings, col.Tokens[2].Postings[0])
					case "wrong_token":
						col.Tokens[2].Postings[0] = col.Tokens[0].Postings[0]
					case "duplicate_posting":
						col.Tokens[0].Postings[1] = col.Tokens[0].Postings[0]
					case "reversed_posting":
						col.Tokens[0].Postings[0], col.Tokens[0].Postings[1] = col.Tokens[0].Postings[1], col.Tokens[0].Postings[0]
					case "row":
						col.Tokens[0].Postings[0].Row = 0
					case "version":
						col.Tokens[0].Postings[0].Version = 0
					case "position":
						col.Tokens[0].Postings[0].Position = -1
					case "empty_list":
						col.Tokens[0].Postings = nil
					case "swapped_token":
						col.Tokens[0], col.Tokens[1] = col.Tokens[1], col.Tokens[0]
					case "duplicate_token":
						col.Tokens[1].Token = col.Tokens[0].Token
					case "category":
						col.Tokens[0].Token.Kind = 255
					case "placeholder_payload":
						col.Tokens[3].Token.Element = col.Tokens[0].Token.Element
					case "placeholder_lexeme":
						columns[1].Tokens[2].Token.Lexeme = "hidden"
					case "null_ordinary":
						col.Tokens[0].Token.Element = sqltype.Null(col.Tokens[0].Token.Element.Type())
					case "vector_element":
						columns[1].Tokens[0].Token.Element = col.Tokens[0].Token.Element
					case "vector_empty_lexeme":
						columns[1].Tokens[0].Token.Lexeme = ""
					case "vector_null_element":
						columns[1].Tokens[2].Token.Kind = sqltype.GinNullElement
					case "element_generation":
						col.Tokens[0].Token.Element = next.Entries[0].Keys[1]
					case "binding":
						col.Binding = columns[1].Binding
					case "columns":
						if pending {
							next.Gin.Pending = columns[:1]
						} else {
							next.Gin.Columns = columns[:1]
						}
					case "arity":
						next.Entries[0].Keys = nil
					case "key_generation":
						next.Entries[0].Keys[0] = next.Entries[0].Keys[1]
					case "entry_owner":
						next.Entries[0].Position = next.Entries[1].Position
					case "duplicate_entry":
						next.Entries[1] = next.Entries[0]
					case "membership":
						next.Membership = map[ids.RowID]ids.VersionID{next.Entries[0].Row: next.Entries[0].Version}
					case "row_entries":
						next.RowEntries = map[ids.RowID]int{next.Entries[0].Row: 999}
					case "missing_nonnull":
						col.NonNull = col.NonNull[1:]
					case "spurious_nonnull":
						col.NonNull = append(col.NonNull, col.Tokens[len(col.Tokens)-1].Postings[0])
					case "reversed_nonnull":
						col.NonNull[0], col.NonNull[1] = col.NonNull[1], col.NonNull[0]
					case "missing_payload":
						next.Gin = nil
					case "mixed_payload":
						next.Hash = &storage.HashRoot{}
					case "overlapping_portions":
						if pending {
							next.Gin.Columns = ginAmcheckCloneColumns(columns)
						} else {
							next.Gin.Pending = ginAmcheckCloneColumns(columns)
						}
					case "cross_column_placement":
						opposite := next.Gin.Columns
						if !pending {
							opposite = make([]storage.GinColumn, len(columns))
							for i := range columns {
								opposite[i].Binding = columns[i].Binding
							}
							next.Gin.Pending = opposite
						}
						opposite[1] = columns[1]
						columns[1].Tokens, columns[1].NonNull = nil, nil
					}
					damagedBefore := ginAmcheckClone(next)
					e.Storage.IndexRoots[d.Address()] = next
					amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT gin_index_check('i')"), sqlstate.Error{Code: "XX002", Message: `GIN posting invariant violated for index "i"`})
					if !reflect.DeepEqual(r, before) || !reflect.DeepEqual(next, damagedBefore) || e.Storage.IndexRoots[d.Address()] != next {
						t.Fatal("checker changed damaged or original authority")
					}
					e.Storage.IndexRoots[d.Address()] = r
					if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
						t.Fatal("healthy recovery", err)
					}
				})
			}
		})
	}
}

func TestAmcheckGINSplitPostings(t *testing.T) {
	e, d, old := ginAmcheckUnitRoot(t)
	oldCopy := ginAmcheckClone(old)
	enginetest.Run(t, e, "INSERT INTO t VALUES(7,'{1,1,2}','cat')")
	_, root := btreeRoot(t, e, "i")
	if root == old || len(root.Gin.Pending) != 2 || storage.GinPendingIncidences(root.Gin) != 3 {
		t.Fatal("missing genuine mixed main/pending dictionary")
	}
	for ordinal := range root.Gin.Columns {
		main, pending := &root.Gin.Columns[ordinal], &root.Gin.Pending[ordinal]
		order, err := main.Binding.Compare(context.Background(), main.Tokens[0].Token, pending.Tokens[0].Token, sqltype.InputEnvironment{})
		if err != nil || order != 0 {
			t.Fatal("fixture lacks a shared token across disjoint portions", ordinal, err)
		}
	}
	before := ginAmcheckClone(root)
	if err := amcheckUnitCall(t, e, "SELECT gin_index_check('i')"); err != nil {
		t.Fatal("healthy split root", err)
	}
	if e.Storage.IndexRoots[d.Address()] != root || !reflect.DeepEqual(root, before) || !reflect.DeepEqual(old, oldCopy) {
		t.Fatal("verification changed current or retained split state")
	}
}
