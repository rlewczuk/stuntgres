package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestGINPresentationRebinding(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE ROLE gin_rebinding_owner",
		"CREATE SCHEMA gin_rebinding_schema",
		"CREATE TABLE gin_rebinding(id integer,a integer[],v tsvector)",
		"INSERT INTO gin_rebinding VALUES(1,'{1,2,2}','cat:1 rat:2')",
		"CREATE INDEX gin_rebinding_i ON gin_rebinding USING gin(a,v)",
	} {
		enginetest.Run(t, e, q)
	}
	type retained struct {
		index   *catalog.Relation
		root    *storage.IndexRoot
		keys    []catalog.IndexKey
		entries []storage.IndexEntry
		payload *storage.GinRoot
	}
	var predecessors []retained
	for _, change := range []struct {
		query  string
		column int
	}{
		{"ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_rebinding_array", 0},
		{"ALTER OPERATOR CLASS pg_catalog.tsvector_ops USING gin RENAME TO gin_rebinding_vector", 1},
		{"ALTER OPERATOR CLASS pg_catalog.gin_rebinding_array USING gin OWNER TO gin_rebinding_owner", 0},
		{"ALTER OPERATOR CLASS pg_catalog.gin_rebinding_vector USING gin OWNER TO gin_rebinding_owner", 1},
		{"ALTER OPERATOR CLASS pg_catalog.gin_rebinding_array USING gin SET SCHEMA gin_rebinding_schema", 0},
		{"ALTER OPERATOR CLASS pg_catalog.gin_rebinding_vector USING gin SET SCHEMA gin_rebinding_schema", 1},
		{"ALTER OPERATOR FAMILY pg_catalog.array_ops USING gin RENAME TO gin_rebinding_array_family", 0},
		{"ALTER OPERATOR FAMILY pg_catalog.tsvector_ops USING gin RENAME TO gin_rebinding_vector_family", 1},
		{"ALTER OPERATOR FAMILY pg_catalog.gin_rebinding_array_family USING gin OWNER TO gin_rebinding_owner", 0},
		{"ALTER OPERATOR FAMILY pg_catalog.gin_rebinding_vector_family USING gin OWNER TO gin_rebinding_owner", 1},
		{"ALTER OPERATOR FAMILY pg_catalog.gin_rebinding_array_family USING gin SET SCHEMA gin_rebinding_schema", 0},
		{"ALTER OPERATOR FAMILY pg_catalog.gin_rebinding_vector_family USING gin SET SCHEMA gin_rebinding_schema", 1},
	} {
		priorIndex, prior := ginInstalledRoot(t, e, "gin_rebinding_i")
		predecessors = append(predecessors, retained{priorIndex, prior, slices.Clone(priorIndex.SQLIndex.Keys), gistCopyEntries(prior), ginCopyPayload(prior.Gin)})
		detached := priorIndex.SQLIndex.Keys[change.column].Class
		enginetest.Run(t, e, change.query)
		currentIndex, current := ginInstalledRoot(t, e, "gin_rebinding_i")
		view := e.CatalogRead()
		if currentIndex == priorIndex || current == prior || current.Gin == prior.Gin {
			t.Fatal("live presentation replacement did not refresh dependent authority", change.query)
		}
		for column, oid := range []ids.OID{10064, 10073} {
			want := ginClass(t, view, oid)
			if currentIndex.SQLIndex.Keys[column].Class != want || current.Gin.Columns[column].Binding.Class != want {
				t.Fatal("live key/root lacks current selected captures", change.query, column)
			}
			oldColumn, newColumn := prior.Gin.Columns[column], current.Gin.Columns[column]
			if oldColumn.Binding.Usage != newColumn.Binding.Usage || &oldColumn.Tokens[0] != &newColumn.Tokens[0] || &oldColumn.NonNull[0] != &newColumn.NonNull[0] {
				t.Fatal("presentation-only refresh rebuilt typed dictionaries or posting backing", change.query, column)
			}
		}
		if &prior.Entries[0] != &current.Entries[0] || index.ConstructionMatches(prior, priorIndex) != index.ConstructionMatches(current, currentIndex) {
			t.Fatal("presentation-only refresh changed entries or construction status", change.query)
		}
		if _, err := routine.BindClassSupport(view, detached); err == nil {
			t.Fatal("authorized live refresh laundered detached stale authority", change.query)
		}
		enginetest.Run(t, e, "INSERT INTO gin_rebinding VALUES(3,'{3,3}','cat:1 rat:2')")
		_, inserted := ginInstalledRoot(t, e, "gin_rebinding_i")
		if got := ginPostingID(t, e, currentIndex, ginLookup(t, inserted.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"})); !slices.Equal(got, []int64{1, 3}) {
			t.Fatal("current presentation authority lost inserted postings", got)
		}
		enginetest.Run(t, e, "UPDATE gin_rebinding SET a='{4}',v='dog:1' WHERE id=3")
		_, updated := ginInstalledRoot(t, e, "gin_rebinding_i")
		if got := ginPostingID(t, e, currentIndex, ginLookup(t, updated.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"})); !slices.Equal(got, []int64{1}) {
			t.Fatal("current presentation authority retained ended vector postings", got)
		}
		array := updated.Gin.Columns[0].Binding.Usage.Type
		three := sqltype.GinToken{Element: ginInput(t, array, "{3}").ArrayElement(0)}
		four := sqltype.GinToken{Element: ginInput(t, array, "{4}").ArrayElement(0)}
		if len(ginLookup(t, updated.Gin.Columns[0], three)) != 0 || !slices.Equal(ginPostingID(t, e, currentIndex, ginLookup(t, updated.Gin.Columns[0], four)), []int64{3}) {
			t.Fatal("current presentation authority lost exact array update")
		}
		enginetest.Run(t, e, "DELETE FROM gin_rebinding WHERE id=3")
		_, deleted := ginInstalledRoot(t, e, "gin_rebinding_i")
		if len(deleted.Entries) != 1 || len(ginLookup(t, deleted.Gin.Columns[0], four)) != 0 || len(ginLookup(t, deleted.Gin.Columns[1], sqltype.GinToken{Lexeme: "dog"})) != 0 {
			t.Fatal("current presentation authority lost exact deletion")
		}
		for _, old := range predecessors {
			if !reflect.DeepEqual(old.index.SQLIndex.Keys, old.keys) || !reflect.DeepEqual(old.root.Entries, old.entries) || !reflect.DeepEqual(old.root.Gin, old.payload) {
				t.Fatal("later presentation/DML mutated detached predecessors", change.query)
			}
		}
	}
	// Current live captures still reject genuinely missing/incompatible support.
	idx, live := ginInstalledRoot(t, e, "gin_rebinding_i")
	view := e.CatalogRead()
	for _, missing := range []bool{false, true} {
		state := catalog.CloneState(view.State())
		member, _ := view.FamilySupport(2745, 2277, 2277, 2)
		if missing {
			state.RemoveDescriptor(member.Address())
		} else {
			changed := *member
			changed.ObjectRevision = member.Revision() + 1
			changed.Function = 333
			state.SetDescriptor(changed.Address(), &changed)
		}
		if _, err := routine.BindClassSupport(view.WithState(state), idx.SQLIndex.Keys[0].Class); err == nil {
			t.Fatal("presentation refresh authorized missing/incompatible support", missing)
		}
		if installed := e.Storage.IndexRoots[idx.Address()]; installed != live {
			t.Fatal("rejected detached binding replaced the installed root")
		}
	}
	// Ordinary private catalog changes must use current captures without
	// exposing their roots or metadata to the committed catalog.
	session := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, session)
	enginetest.SessionSQL(t, session, "INSERT INTO gin_rebinding VALUES(8,'{8}','prefix:1')")
	priorWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
	enginetest.SessionSQL(t, session, "ALTER OPERATOR CLASS gin_rebinding_schema.gin_rebinding_array USING gin RENAME TO gin_rebinding_private")
	e.Mu.Lock()
	privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
	privateIndex := privateView.MustRelation(idx.Address())
	privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
	e.Mu.Unlock()
	if privateRoot == nil || privateRoot == live || &privateRoot.Gin.Columns[0].Tokens[0] != &live.Gin.Columns[0].Tokens[0] || &privateRoot.Entries[0] != &live.Entries[0] {
		t.Fatal("private presentation change rebuilt or borrowed stale baseline authority")
	}
	for column, oid := range []ids.OID{10064, 10073} {
		current := ginClass(t, privateView, oid)
		if privateIndex.SQLIndex.Keys[column].Class != current || privateRoot.Gin.Columns[column].Binding.Class != current {
			t.Fatal("private presentation change omitted current key/root captures", column)
		}
	}
	currentWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
	if len(currentWrites) != len(priorWrites) || &currentWrites[0].Values[0] != &priorWrites[0].Values[0] {
		t.Fatal("presentation change reconstructed an earlier own-write payload")
	}
	if output, exists := priorWrites[0].IndexOutputs[idx.Address()]; exists {
		current := currentWrites[0].IndexOutputs[idx.Address()]
		if output.Index != idx || current.Index != privateIndex || !slices.Equal(output.Keys, current.Keys) || len(output.Keys) != 0 && &output.Keys[0] != &current.Keys[0] {
			t.Fatal("presentation change lost produced own-write authority/backing")
		}
	}
	enginetest.SessionSQL(t, session, "INSERT INTO gin_rebinding VALUES(9,'{9}','private:1')")
	if e.CatalogRead().State() != view.State() || e.Storage.IndexRoots[idx.Address()] != live {
		t.Fatal("private presentation/DML published committed catalog or root")
	}
	enginetest.TransactionEnd(t, session, tx, false)
	rolledBackIndex, rolledBack := ginInstalledRoot(t, e, "gin_rebinding_i")
	if rolledBackIndex != idx || rolledBack != live || e.CatalogRead().State() != view.State() {
		t.Fatal("rolled-back presentation change replaced committed authority")
	}
	if len(ginLookup(t, rolledBack.Gin.Columns[1], sqltype.GinToken{Lexeme: "private"})) != 0 {
		t.Fatal("rolled-back presentation change published private postings")
	}
}

func ginDictionaryFixture(t *testing.T, tokens int) ginFixture {
	t.Helper()
	f := newGinFixture(t)
	array := f.parent.Columns[0].Usage.Type
	elements := make([]sqltype.Value, tokens)
	for i := range elements {
		var err error
		elements[i], err = sqltype.Integer(array.Element(), int64(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: int32(tokens)}}, elements)
	if err != nil {
		t.Fatal(err)
	}
	f.store.Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value, ginInput(t, f.parent.Columns[1].Usage.Type, "cat:1")}}}
	f.root, err = index.BuildIndexRoot(f.evaluation, f.parent, f.descriptor.SQLIndex, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.root.Entries) != 1 || len(f.root.Gin.Columns[0].Tokens) != tokens {
		t.Fatal("dictionary fixture did not produce exact physical inventory", tokens)
	}
	ginAssertOwners(t, f.root, f.store)
	return f
}

func ginAdmissionDelta(t *testing.T, f ginFixture) *index.RowPublication {
	t.Helper()
	return &index.RowPublication{Store: f.store, Root: f.store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{{Row: 20, Version: 20, Values: []sqltype.Value{
		ginInput(t, f.parent.Columns[0].Usage.Type, "{100000,100001,100001}"),
		ginInput(t, f.parent.Columns[1].Usage.Type, "zebra:1"),
	}}}}
}

func TestGINNonmaterializingMerge(t *testing.T) {
	small, large := ginDictionaryFixture(t, 8), ginDictionaryFixture(t, 8192)
	p := ginAdmissionDelta(t, large)
	before, entries := ginCopyPayload(large.root.Gin), gistCopyEntries(large.root)
	t.Run("required-validation", func(t *testing.T) {
		if candidate, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, large.root, p, true, false); candidate != nil || err != nil {
			t.Fatal("nonmaterializing admission returned a candidate or error", candidate, err)
		}
		foreign := engine.New().CatalogRead()
		for column, typeID := range []sqltype.ID{1007, sqltype.TSVector} {
			typ, _ := foreign.LookupType(typeID)
			text := "{100000}"
			if column == 1 {
				text = "zebra:1"
			}
			keys := slices.Clone(p.Writes[0].Values)
			keys[column] = ginInput(t, typ, text)
			bad := *p
			row := p.Writes[0]
			row.IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{large.descriptor.Address(): {Index: large.descriptor, Member: true, Keys: keys}}
			bad.Writes = []storage.RowVersion{row}
			if candidate, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, large.root, &bad, true, false); candidate != nil || err == nil {
				t.Fatal("nonmaterializing admission skipped produced-token extraction", column, candidate, err)
			}
		}
		state := catalog.CloneState(large.view.State())
		state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2743})
		evaluation := *large.evaluation
		evaluation.Catalog = large.view.WithState(state)
		if candidate, err := index.MergeGinIndexRoot(&evaluation, large.parent, large.descriptor, large.root, p, true, false); candidate != nil || err == nil {
			t.Fatal("nonmaterializing admission skipped current provider validation", candidate, err)
		}
		// A high added token must still encounter fallible comparisons against
		// the incumbent dictionary, even though no merged directory is wanted.
		badRoot := *large.root
		badRoot.Gin = ginCopyPayload(large.root.Gin)
		last := len(badRoot.Gin.Columns[0].Tokens) - 1
		foreignArray, _ := foreign.LookupType(1007)
		badRoot.Gin.Columns[0].Tokens[last].Token.Element = ginInput(t, foreignArray, "{8191}").ArrayElement(0)
		if candidate, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, &badRoot, p, true, false); candidate != nil || err == nil {
			t.Fatal("nonmaterializing admission skipped incumbent token comparison", candidate, err)
		}
		// The last budget exceeds constant-delta extraction/sorting work and
		// interrupts the required incumbent-dictionary validation traversal.
		for _, polls := range []int{1, 10, 1000} {
			evaluation := *large.evaluation
			ctx := &spgistInterruptContext{Context: context.Background(), remaining: polls}
			evaluation.Context = ctx
			if candidate, err := index.MergeGinIndexRoot(&evaluation, large.parent, large.descriptor, large.root, p, true, false); candidate != nil || !errors.Is(err, context.Canceled) || ctx.remaining > 0 {
				t.Fatal("nonmaterializing cancellation escaped admission", polls, candidate, err)
			}
		}
		materialized, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, large.root, p, true, true)
		if err != nil || materialized == nil || materialized.Gin == nil {
			t.Fatal("same valid delta did not produce a materializing control", err)
		}
		ginWantRows(t, ginLookup(t, materialized.Gin.Columns[1], sqltype.GinToken{Lexeme: "zebra"}), 20)
		deletion := &index.RowPublication{Store: large.store, Root: large.store.Versions, SingleSlot: -1, Deletion: &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Row: 1, Version: 1}}}}
		if candidate, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, large.root, deletion, true, false); candidate != nil || err != nil {
			t.Fatal("nonmaterializing delete-only admission", candidate, err)
		}
		empty, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, large.root, deletion, true, true)
		if err != nil || empty == nil || empty.Gin == nil || len(empty.Entries) != 0 || len(empty.Gin.Columns[0].Tokens) != 0 || len(empty.Gin.Columns[1].NonNull) != 0 {
			t.Fatal("delete-only materializing control retained ended data", empty, err)
		}
	})
	t.Run("bounded-allocation", func(t *testing.T) {
		measure := func(f ginFixture, mode string) int64 {
			delta := ginAdmissionDelta(t, f)
			if mode != "append" {
				delta = &index.RowPublication{Store: f.store, Root: f.store.Versions, SingleSlot: -1}
				if mode == "delete" {
					delta.Deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Row: 1, Version: 1}}}
				}
			}
			var candidate *storage.IndexRoot
			var err error
			_, bytes := util.AllocationsPerCall(3, func() {
				candidate, err = index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, f.root, delta, true, false)
			})
			if candidate != nil || err != nil {
				t.Fatal("allocation fixture failed admission", candidate, err)
			}
			return bytes
		}
		for _, mode := range []string{"append", "delete", "nochange"} {
			smallBytes, largeBytes := measure(small, mode), measure(large, mode)
			// Heap/key count and delta size are identical. Allow fixed validation
			// scratch, but less growth than even one pointer per incumbent token:
			// a merged GinPostingList directory cannot fit within this allowance.
			if largeBytes > smallBytes+32*1024 {
				t.Fatalf("nonmaterializing %s allocated installation-sized output: small=%d large=%d bytes/call", mode, smallBytes, largeBytes)
			}
		}
	})
	if !reflect.DeepEqual(large.root.Gin, before) || !reflect.DeepEqual(large.root.Entries, entries) || p.Next != nil || p.NextEnds != nil || p.Slots != nil {
		t.Fatal("nonmaterializing validation mutated predecessor/publication state")
	}
}
