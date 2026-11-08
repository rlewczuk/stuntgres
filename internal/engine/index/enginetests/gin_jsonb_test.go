package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

var ginJSONBValues = [...]string{
	`{"a":"b","n":1,"arr":[1,2],"o":{"k":"v"}}`,
	`{"a":"c","n":1.0,"arr":[3]}`,
	`{}`,
	"", // SQL NULL row 4
	`null`,
	`[1,2,3]`,
	`["a","b"]`,
	`{"a":null}`,
	`{"x":{"a":1}}`,
	`{"a":{"b":1}}`,
}

func newGinJSONBFixture(t *testing.T) ginFixture {
	t.Helper()
	view := engine.New().CatalogRead()
	jsonb, ok := view.LookupType(sqltype.JSONB)
	if !ok {
		t.Fatal("missing jsonb type")
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 92000}, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "labels", Usage: jsonb.Usage()}}}
	d := &catalog.IndexDefinition{Method: catalog.GinMethod, MethodOID: 2742, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: ginClass(t, view, 10090)}}, Ready: true, Valid: true, Live: true}
	descriptor := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 92001}, SQLIndex: d}
	store := &storage.Relation{}
	for i, text := range ginJSONBValues {
		value := sqltype.Null(jsonb)
		if text != "" {
			value = ginInput(t, jsonb, text)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: []sqltype.Value{value}})
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, parent, d, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ginFixture{view, parent, descriptor, store, evaluation, root}
}

func ginJSONBValue(t *testing.T, f ginFixture, id sqltype.ID, text string) sqltype.Value {
	t.Helper()
	d, ok := f.view.LookupType(id)
	if !ok {
		t.Fatal("missing operand type", id)
	}
	return ginInput(t, d, text)
}

func ginJSONBPositions(t *testing.T, column storage.GinColumn, lexeme string) []storage.GinPosting {
	t.Helper()
	got := ginLookup(t, column, sqltype.GinToken{Lexeme: lexeme})
	return got
}

func TestGINJSONBDictionary(t *testing.T) {
	f := newGinJSONBFixture(t)
	if f.root.Gin == nil || len(f.root.Gin.Columns) != 1 || len(f.root.Gin.Pending) != 0 {
		t.Fatal("not a substantive jsonb root", f.root.Gin)
	}
	column := f.root.Gin.Columns[0]
	if !column.Binding.IsJSONB() || column.Binding.JSONBPathOps() || column.Binding.IsScalar() {
		t.Fatal("jsonb_ops binding", column.Binding)
	}
	if n := len(column.NonNull); n != 9 {
		t.Fatal("every non-SQL-NULL row is non-null", n)
	}
	for _, p := range column.NonNull {
		if p.Row == 4 {
			t.Fatal("SQL NULL row entered NonNull", p)
		}
	}
	// Empty root {} contributes no keys or postings; root 'null' contributes a
	// JSON-null token distinct from the SQL NULL GinNullItem category.
	for _, list := range column.Tokens {
		for _, p := range list.Postings {
			if p.Row == 3 {
				t.Fatal("empty root produced a token", list.Token)
			}
		}
	}
	if got := ginJSONBPositions(t, column, "\x01a"); !slices.Equal(got, []storage.GinPosting{{Row: 1, Version: 1, Position: 0}, {Row: 2, Version: 2, Position: 1}, {Row: 7, Version: 7, Position: 6}, {Row: 8, Version: 8, Position: 7}, {Row: 9, Version: 9, Position: 8}, {Row: 10, Version: 10, Position: 9}}) {
		t.Fatal("KEY a postings", got)
	}
	if got := ginJSONBPositions(t, column, "\x05b"); !slices.Equal(got, []storage.GinPosting{{Row: 1, Version: 1, Position: 0}}) {
		t.Fatal("STR b postings", got)
	}
	if got := ginJSONBPositions(t, column, "\x041"); !slices.Equal(got, []storage.GinPosting{{Row: 1, Version: 1, Position: 0}, {Row: 2, Version: 2, Position: 1}, {Row: 6, Version: 6, Position: 5}, {Row: 9, Version: 9, Position: 8}, {Row: 10, Version: 10, Position: 9}}) {
		t.Fatal("NUM 1 postings", got)
	}
	ginWantRows(t, ginJSONBPositions(t, column, "\x02"), 5, 8)
	ginWantRows(t, ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}), 4)

	jsonb, _ := f.view.LookupType(sqltype.JSONB)
	text, _ := f.view.LookupType(sqltype.Text)
	textArray, _ := f.view.LookupType(1009)
	jsonpath, _ := f.view.LookupType(sqltype.JSONPath)
	allNonNull := []ids.RowID{1, 2, 3, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		strategy int
		query    sqltype.Value
		want     []ids.RowID
	}{
		{7, ginJSONBValue(t, f, sqltype.JSONB, `{"a":"b"}`), []ids.RowID{1}},
		{7, ginJSONBValue(t, f, sqltype.JSONB, `{"n":1}`), []ids.RowID{1, 2}},
		{7, ginJSONBValue(t, f, sqltype.JSONB, `{"a":null}`), []ids.RowID{8}},
		{7, ginJSONBValue(t, f, sqltype.JSONB, `{}`), allNonNull},
		{7, ginJSONBValue(t, f, sqltype.JSONB, `[3]`), []ids.RowID{2, 6}},
		{7, ginJSONBValue(t, f, sqltype.JSONB, `{"o":{"k":"v"}}`), []ids.RowID{1}},
		{9, ginInput(t, text, "a"), []ids.RowID{1, 2, 7, 8, 9, 10}},
		{9, ginInput(t, text, "b"), []ids.RowID{7, 10}},
		{9, ginInput(t, text, "k"), []ids.RowID{1}},
		{10, ginInput(t, textArray, "{a,zzz}"), []ids.RowID{1, 2, 7, 8, 9, 10}},
		{10, ginInput(t, textArray, "{}"), nil},
		{11, ginInput(t, textArray, "{a,n}"), []ids.RowID{1, 2}},
		{11, ginInput(t, textArray, "{a,b}"), []ids.RowID{7, 10}},
		{11, ginInput(t, textArray, "{}"), allNonNull},
		{15, ginInput(t, jsonpath, "$.a"), allNonNull},
		{16, ginInput(t, jsonpath, "$.n == 1"), allNonNull},
	} {
		got, err := index.GinJSONBCandidates(context.Background(), f.root.Gin, 0, tc.strategy, tc.query, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(tc.strategy, err)
		}
		ginWantRows(t, got, tc.want...)
	}

	// Pending and main portions are unioned by exact row/version identity.
	binding := f.root.Gin.Columns[0].Binding
	pendingValue := ginJSONBValue(t, f, sqltype.JSONB, `{"a":"b"}`)
	pending, err := storage.BuildGin(context.Background(), []sqltype.GinBinding{binding}, []storage.IndexEntry{{Row: 11, Version: 11, Position: 10, Keys: []sqltype.Value{pendingValue}}}, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	probe := &storage.GinRoot{Columns: f.root.Gin.Columns, Pending: pending.Columns}
	got, err := index.GinJSONBCandidates(context.Background(), probe, 0, 7, pendingValue, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, got, 1, 11)
	got, err = index.GinJSONBCandidates(context.Background(), f.root.Gin, 0, 7, pendingValue, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, got, 1)

	// jsonb_path_ops covers @> and rejects the ? family outright.
	pathClass := ginClass(t, f.view, 10091)
	pathBinding := ginBinding(t, jsonb, pathClass)
	if !pathBinding.JSONBPathOps() {
		t.Fatal("path_ops discriminator", pathBinding)
	}
	entries := make([]storage.IndexEntry, len(ginJSONBValues))
	for i, text := range ginJSONBValues {
		value := sqltype.Null(jsonb)
		if text != "" {
			value = ginInput(t, jsonb, text)
		}
		entries[i] = storage.IndexEntry{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Position: i, Keys: []sqltype.Value{value}}
	}
	pathRoot, err := storage.BuildGin(context.Background(), []sqltype.GinBinding{pathBinding}, entries, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range pathRoot.Columns[0].Tokens {
		if list.Token.Kind != sqltype.GinOrdinary {
			continue
		}
		if len(list.Token.Lexeme) != 4 {
			t.Fatal("path dictionary token width", list.Token.Lexeme)
		}
	}
	got, err = index.GinJSONBCandidates(context.Background(), pathRoot, 0, 7, ginJSONBValue(t, f, sqltype.JSONB, `{"n":1}`), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, got, 1, 2)
	if got, err := index.GinJSONBCandidates(context.Background(), pathRoot, 0, 9, ginInput(t, text, "a"), sqltype.InputEnvironment{}); err == nil || got != nil {
		t.Fatal("path_ops ? probe accepted", got, err)
	}
	if _, err := index.GinJSONBCandidates(context.Background(), f.root.Gin, 1, 7, pendingValue, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("foreign ordinal accepted")
	}
}

func TestGINJSONBMaintenance(t *testing.T) {
	f := newGinJSONBFixture(t)
	jsonb, _ := f.view.LookupType(sqltype.JSONB)
	if !f.root.Gin.Columns[0].Binding.Same(f.root.Gin.Columns[0].Binding) {
		t.Fatal("binding identity")
	}
	add := func(text string, pending bool) (*storage.IndexRoot, *index.RowPublication) {
		t.Helper()
		value := ginInput(t, jsonb, text)
		row := storage.RowVersion{Row: 100, Version: 100, Values: []sqltype.Value{value}, IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{f.descriptor.Address(): {Index: f.descriptor, Member: true, Keys: []sqltype.Value{value}, GinPending: pending}}}
		publication := &index.RowPublication{Writes: []storage.RowVersion{row}, Root: f.store.Versions, Store: f.store, SingleSlot: -1}
		root, err := index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, f.root, publication, false, true)
		if err != nil {
			t.Fatal(err)
		}
		return root, publication
	}
	root, publication := add(`{"a":"z"}`, true)
	if len(root.Gin.Pending) != 1 || storage.GinPendingIncidences(root.Gin) == 0 || !root.Gin.Columns[0].Binding.Same(f.root.Gin.Columns[0].Binding) || !root.Gin.Pending[0].Binding.Same(f.root.Gin.Columns[0].Binding) {
		t.Fatal("deferred jsonb placement", root.Gin)
	}
	ginWantRows(t, ginJSONBPositions(t, root.Gin.Pending[0], "\x01a"), 100)
	cleaned, n, err := storage.CleanGin(context.Background(), root.Gin, sqltype.InputEnvironment{})
	if err != nil || n == 0 || len(cleaned.Pending) != 0 || storage.GinPendingIncidences(cleaned) != 0 {
		t.Fatal("cleanup", n, err, cleaned)
	}
	if !cleaned.Columns[0].Binding.Same(f.root.Gin.Columns[0].Binding) {
		t.Fatal("cleanup changed the binding")
	}
	ginWantRows(t, ginJSONBPositions(t, cleaned.Columns[0], "\x01a"), 1, 2, 7, 8, 9, 10, 100)
	// A non-pending output routes straight to main with the same binding.
	off, _ := add(`{"a":"z"}`, false)
	if storage.GinPendingIncidences(off.Gin) != 0 {
		t.Fatal("main placement produced pending work")
	}
	ginWantRows(t, ginJSONBPositions(t, off.Gin.Columns[0], "\x01a"), 1, 2, 7, 8, 9, 10, 100)
	ended, err := storage.MergeGin(context.Background(), off.Gin, nil, nil, map[ids.VersionID]ids.RowID{100: 100}, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, ginJSONBPositions(t, ended.Columns[0], "\x01a"), 1, 2, 7, 8, 9, 10)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation := *f.evaluation
	evaluation.Context = canceled
	if candidate, err := index.MergeGinIndexRoot(&evaluation, f.parent, f.descriptor, f.root, publication, false, true); candidate != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled merge", candidate, err)
	}
	if candidate, _, err := storage.CleanGin(canceled, root.Gin, sqltype.InputEnvironment{}); candidate != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cleanup", candidate, err)
	}
}