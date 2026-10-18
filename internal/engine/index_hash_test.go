package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func hashRoot(t *testing.T, e *Engine, name string) (*catalog.Relation, *hashIndexRoot) {
	t.Helper()
	index, carrier := btreeRoot(t, e, name)
	if index.SQLIndex.Method != catalog.HashMethod || carrier.hash == nil || len(carrier.entries) != 0 {
		t.Fatal("not a hash root", index, carrier)
	}
	return index, carrier.hash
}
func hashKeys(t *testing.T, root *hashIndexRoot) []string {
	t.Helper()
	keys := []string{}
	versions := map[ids.VersionID]bool{}
	for _, bucket := range root.buckets {
		for _, entry := range bucket {
			if entry.key.IsNull() || entry.row == 0 || entry.version == 0 || versions[entry.version] || entry.position < 0 || entry.position >= root.prefix {
				t.Fatal("invalid/collapsed hash identity", entry)
			}
			versions[entry.version] = true
			s, err := entry.key.Type().Output(context.Background(), entry.key)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, s)
		}
	}
	slices.Sort(keys)
	return keys
}

func TestHashIndexImmutableBatchAndCapturedPrefixes(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1),(2),(2),(NULL),(3)", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	_, old := hashRoot(t, e, "hn")
	query, source, release := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=2")
	value := source.committed[1].values[0]
	h, err := old.binding.provider.Hash(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "UPDATE t SET n=4 WHERE n=1")
	_, updated := hashRoot(t, e, "hn")
	if old == updated || &old.buckets[h][0] != &updated.buckets[h][0] {
		t.Fatal("untouched bucket copied or root reused")
	}
	if got := hashKeys(t, old); !reflect.DeepEqual(got, []string{"1", "2", "2", "3"}) {
		t.Fatal("old root mutated", got)
	}
	if got := hashKeys(t, updated); !reflect.DeepEqual(got, []string{"2", "2", "3", "4"}) {
		t.Fatal(got)
	}
	run(t, e, "DELETE FROM t WHERE n=2")
	run(t, e, "INSERT INTO t VALUES(2),(NULL)")
	stage := hashFilterStage(t, query, source, nil)
	if stage.hashCandidates == nil || stage.hashCandidates.root != old || len(stage.hashCandidates.occurrences) != 2 {
		t.Fatal("probe reread latest generation/root")
	}
	if err := stage.drain(context.Background()); err != nil || stage.count() != 2 {
		t.Fatal("retained prefix lost old identities", stage.count(), err)
	}
	stage.Close()
	release()
	_, current := hashRoot(t, e, "hn")
	if got := hashKeys(t, current); !reflect.DeepEqual(got, []string{"2", "3", "4"}) {
		t.Fatal(got)
	}
	freshQuery, freshSource, releaseFresh := hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=2")
	fresh := hashFilterStage(t, freshQuery, freshSource, nil)
	if fresh.hashCandidates == nil || len(fresh.hashCandidates.occurrences) != 1 {
		t.Fatal("new capture used old candidates")
	}
	fresh.Close()
	releaseFresh()
	retained := current
	for _, q := range []string{"INSERT INTO t VALUES('bad')", "CREATE INDEX bad ON t USING hash((10/(n-2)))"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		if err == nil {
			t.Fatal("invalid operation succeeded", q)
		}
		_, root := hashRoot(t, e, "hn")
		if root != retained {
			t.Fatal("failure changed existing root", q)
		}
	}
}

func TestHashIndexEmptyBindingCancellationAndPrivateFallback(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(n INT)")
	run(t, e, "CREATE INDEX hn ON t USING hash(n)")
	index, root := hashRoot(t, e, "hn")
	if len(root.buckets) != 0 {
		t.Fatal(root)
	}
	parent := e.catalog.state.MustRelation(index.SQLIndex.Parent)
	bad := *index.SQLIndex
	bad.Keys = append([]catalog.IndexKey(nil), bad.Keys...)
	bad.Keys[0].Class, _ = sqltype.LookupHashIndexClass("int8_ops")
	if _, err := buildHashIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, &bad, e.storage[parent.Address()], true, nil); err == nil {
		t.Fatal("empty build did not eagerly reject provider")
	}
	run(t, e, "INSERT INTO t VALUES(NULL),(NULL)")
	_, nulls := hashRoot(t, e, "hn")
	if len(nulls.buckets) != 0 {
		t.Fatal("NULL indexed", nulls)
	}
	if _, err := buildHashIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, &bad, e.storage[parent.Address()], true, nil); err == nil {
		t.Fatal("all-NULL build skipped provider admission")
	}
	run(t, e, "INSERT INTO t VALUES(2),(2),(3)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	during := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 5}
	if candidate, err := buildHashIndexRoot(&EvalContext{Context: during, Scope: 1, Frame: 1}, parent, index.SQLIndex, e.storage[parent.Address()], true, nil); candidate != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(candidate, err)
	}
	_, retained := hashRoot(t, e, "hn")
	s := sessionStart(t, e, nil)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO t VALUES(2)")
	op := transactionOperation(t, s)
	t.Cleanup(op.Done)
	query, source, release := hashCaptureQuery(t, e, op, "SELECT n FROM t WHERE n=2")
	if len(source.hashIndexes) != 0 {
		t.Fatal("private/explicit view acquired current root")
	}
	stage := hashFilterStage(t, query, source, nil)
	if stage.hashCandidates != nil {
		t.Fatal("private view probed")
	}
	if err := stage.drain(context.Background()); err != nil || stage.count() != 3 {
		t.Fatal(stage.count(), err)
	}
	stage.Close()
	release()
	op.Done()
	transactionEnd(t, s, tx, false)
	_, after := hashRoot(t, e, "hn")
	if after != retained {
		t.Fatal("rollback published private hash root")
	}
	// A generation mismatch is a proof failure, not permission to prune.
	e.mu.Lock()
	e.storage[parent.Address()].generation++
	e.mu.Unlock()
	_, source, release = hashImplicitQuery(t, e, "SELECT n FROM t WHERE n=2")
	if len(source.hashIndexes) != 0 {
		t.Fatal("mismatched generation admitted")
	}
	release()
}

func TestHashIndexMetadataAndForeignReplacement(t *testing.T) {
	source, target := New(), New()
	for _, q := range []string{"CREATE TABLE t(extra INT,n INT,s TEXT)", "INSERT INTO t VALUES(0,2,'B'),(0,1,'a'),(0,2,'B'),(0,NULL,NULL)", "CREATE INDEX hn ON t USING hash(n)", "CREATE INDEX hs ON t USING hash(lower(s))", "CREATE TABLE u(n INT UNIQUE)"} {
		run(t, source, q)
	}
	for _, tc := range []struct {
		name             string
		attribute        int16
		class, collation ids.OID
		definition       string
	}{
		{"hn", 2, 10020, 0, "CREATE INDEX hn ON public.t USING hash (n)"}, {"hs", 0, 10037, 100, "CREATE INDEX hs ON public.t USING hash (lower(s))"},
	} {
		index, _ := hashRoot(t, source, tc.name)
		got, names := indexMaintenanceMetadata(t, source, source.CatalogRead(), tc.name)
		want := indexMaintenanceMetadataFlags(index.Address().ObjectID, index.SQLIndex.Parent.ObjectID, 1)
		want.IndKey = []int16{tc.attribute}
		want.IndClass = []ids.OID{tc.class}
		want.IndCollation = []ids.OID{tc.collation}
		want.IndOption = []int16{0}
		if tc.attribute == 0 {
			want.IndexExpressions = []expr.Expr{index.SQLIndex.Keys[0].Derived.Definition}
		}
		if !reflect.DeepEqual(got, want) || names != (indexesMetadata{SchemaName: "public", TableName: "t", IndexName: tc.name, IndexDef: tc.definition}) {
			t.Fatal(got, names, want)
		}
	}
	backing, _ := btreeRoot(t, source, "u_n_key")
	if backing.SQLIndex.Method != catalog.BtreeMethod {
		t.Fatal("UNIQUE changed method")
	}
	_, old := hashRoot(t, source, "hn")
	run(t, target, "CREATE TABLE old(n INT)")
	run(t, target, "INSERT INTO old VALUES(9),(9),(9),(9),(9)")
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	freshIndex, fresh := hashRoot(t, target, "hn")
	store := target.storage[freshIndex.SQLIndex.Parent]
	if fresh == old || fresh.storage == old.storage || fresh.storage != store || fresh.generation != store.generation || fresh.prefix != 4 {
		t.Fatal("replacement retained source authority", fresh)
	}
	ids := map[ids.RowID]bool{}
	for _, bucket := range old.buckets {
		for _, entry := range bucket {
			ids[entry.row] = true
		}
	}
	for _, bucket := range fresh.buckets {
		for _, entry := range bucket {
			if ids[entry.row] {
				t.Fatal("foreign replacement retained source identity")
			}
		}
	}
	run(t, target, "INSERT INTO t VALUES(0,0,'new')")
	if got := hashKeys(t, old); !reflect.DeepEqual(got, []string{"1", "2", "2"}) {
		t.Fatal("destination mutated source", got)
	}
	previous := fresh
	if _, err := target.ReplaceCatalog(context.Background(), target, target.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	_, rebuilt := hashRoot(t, target, "hn")
	if rebuilt == previous {
		t.Fatal("self replacement reused root")
	}
	if got := hashKeys(t, rebuilt); !reflect.DeepEqual(got, []string{"0", "1", "2", "2"}) {
		t.Fatal(got)
	}
}

func TestHashIndexValidationOnlyPublication(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1),(2)", "CREATE INDEX hn ON t USING hash(n)"} {
		run(t, e, q)
	}
	index, old := hashRoot(t, e, "hn")
	parent := e.catalog.state.MustRelation(index.SQLIndex.Parent)
	store := e.storage[parent.Address()]
	value, err := parent.Columns[0].Usage.Type.Input(context.Background(), "3", -1)
	if err != nil {
		t.Fatal(err)
	}
	writes := append([]rowVersion(nil), store.versions...)
	for i := range writes {
		writes[i].base, writes[i].born = writes[i].version, 0
		writes[i].version += 100
		writes[i].values = []sqltype.Value{value}
	}
	for _, rebuild := range []bool{false, true} {
		for _, materialize := range []bool{false, true} {
			p := rowPublication{address: parent.Address(), store: store, original: store, originalRelation: parent, relation: parent, root: store.versions, writes: writes, ends: store.ends, generation: store.generation, singleSlot: -1}
			if err := e.prepareRowPublication(&p, ids.TxRef{}, materialize); err != nil {
				t.Fatal(err)
			}
			var intents map[ids.ObjectAddress]*catalog.Relation
			if rebuild {
				intents = map[ids.ObjectAddress]*catalog.Relation{index.Address(): index}
			}
			roots, err := e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, e.catalog.state, e.catalog.state, captureIndexStores(e.storage, e.catalog.state), e.indexRoots, nil, intents, []rowPublication{p}, materialize, sqltype.OutputEnvironment{}, nil, nil, nil)
			if err != nil {
				t.Fatal(rebuild, materialize, err)
			}
			if materialize {
				if roots[index.Address()] == e.indexRoots[index.Address()] || roots[index.Address()].hash == nil || !reflect.DeepEqual(hashKeys(t, roots[index.Address()].hash), []string{"3", "3"}) {
					t.Fatal("materializing phase lost hash updates", rebuild, roots)
				}
			} else if roots != nil || p.next != nil || p.nextEnds != nil || p.slots != nil || p.singleSlot != -1 {
				t.Fatal("validation phase constructed installation state", rebuild, roots, p)
			}
			if e.indexRoots[index.Address()].hash != old || store.generation != old.generation || store.ends != old.ends || !reflect.DeepEqual(hashKeys(t, old), []string{"1", "2"}) || store.versions[0].values[0] == value || store.versions[1].values[0] == value {
				t.Fatal("preparation changed committed rows/root", rebuild, materialize)
			}
		}
	}
}
