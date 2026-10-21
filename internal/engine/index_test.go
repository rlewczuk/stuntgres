package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func btreeRoot(t *testing.T, e *Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := roleTableRef(t, e, name)
	idx := e.catalog.state.MustRelation(ref.Address)
	root := e.storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}
func btreeKeys(t *testing.T, root *storage.IndexRoot) []string {
	t.Helper()
	keys := make([]string, len(root.Entries))
	identities := make(map[ids.VersionID]bool)
	for i, entry := range root.Entries {
		if entry.Row == 0 || entry.Version == 0 || identities[entry.Version] {
			t.Fatal("collapsed/invalid identity", entry)
		}
		identities[entry.Version] = true
		value := entry.Keys[0]
		if value.IsNull() {
			keys[i] = "NULL"
		} else {
			var err error
			keys[i], err = value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}
func btreeWantKeys(t *testing.T, e *Engine, name string, want ...string) *storage.IndexRoot {
	t.Helper()
	_, root := btreeRoot(t, e, name)
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	return root
}
func TestBtreeIndexBuildAndBatchMaintenance(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(n INT,s TEXT)")
	run(t, e, "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')")
	run(t, e, "CREATE INDEX n_idx ON t(n)")
	run(t, e, "CREATE INDEX s_idx ON t USING btree(s DESC NULLS LAST,n)")
	old := btreeWantKeys(t, e, "n_idx", "1", "2", "2", "3", "NULL")
	index, descending := btreeRoot(t, e, "s_idx")
	if len(index.SQLIndex.Keys) != 2 || !index.SQLIndex.Keys[0].Desc || index.SQLIndex.Keys[0].NullsFirst {
		t.Fatal(index.SQLIndex)
	}
	if got := btreeKeys(t, descending); !reflect.DeepEqual(got, []string{"c", "b", "b", "a", "NULL"}) {
		t.Fatal(got)
	}
	run(t, e, "INSERT INTO t VALUES(2,'b'),(0,''),(NULL,NULL)")
	run(t, e, "INSERT INTO t SELECT n,s FROM t WHERE n=3")
	btreeWantKeys(t, e, "n_idx", "0", "1", "2", "2", "2", "3", "3", "NULL", "NULL")
	if got := btreeKeys(t, old); !reflect.DeepEqual(got, []string{"1", "2", "2", "3", "NULL"}) {
		t.Fatal("mutated retained root", got)
	}
	run(t, e, "UPDATE t SET n=10 WHERE n=1")
	btreeWantKeys(t, e, "n_idx", "0", "2", "2", "2", "3", "3", "10", "NULL", "NULL")
	run(t, e, "DELETE FROM t WHERE n=3")
	btreeWantKeys(t, e, "n_idx", "0", "2", "2", "2", "10", "NULL", "NULL")
}
func TestBtreeIndexAtomicFailureAndPrivateWrites(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT NOT NULL CHECK(n>=0))")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	sessionSQL(t, s, "CREATE INDEX idx ON t(n)")
	old := btreeWantKeys(t, e, "idx", "1", "2")
	for _, q := range []string{"INSERT INTO t VALUES(3),(NULL)", "INSERT INTO t VALUES(3),(-1)"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		if err == nil {
			t.Fatal("invalid batch succeeded", q)
		}
		if _, root := btreeRoot(t, e, "idx"); root != old {
			t.Fatal("failed batch changed root")
		}
	}
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO t VALUES(3),(3)")
	if _, root := btreeRoot(t, e, "idx"); root != old {
		t.Fatal("private writes published")
	}
	sessionSQL(t, s, "SAVEPOINT before_update")
	sessionSQL(t, s, "UPDATE t SET n=4 WHERE n=2")
	sessionSQL(t, s, "ROLLBACK TO before_update")
	transactionEnd(t, s, tx, true)
	btreeWantKeys(t, e, "idx", "1", "2", "3", "3")
	tx = transactionBegin(t, s)
	sessionSQL(t, s, "DELETE FROM t WHERE n=3")
	transactionEnd(t, s, tx, false)
	btreeWantKeys(t, e, "idx", "1", "2", "3", "3")
}
func TestBtreeIndexLifecycleAndReplacement(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(extra INT,n INT,s TEXT)")
	run(t, e, "INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')")
	run(t, e, "CREATE INDEX ni ON t(n)")
	run(t, e, "CREATE INDEX si ON t(s)")
	_, root := btreeRoot(t, e, "ni")
	run(t, e, "ALTER TABLE t RENAME n TO key")
	run(t, e, "ALTER TABLE t RENAME TO renamed")
	_, same := btreeRoot(t, e, "ni")
	if root != same {
		t.Fatal("rename rebuilt entries")
	}
	run(t, e, "ALTER TABLE renamed DROP extra")
	btreeWantKeys(t, e, "ni", "1", "2", "10")
	run(t, e, "ALTER TABLE renamed ALTER key TYPE TEXT USING key::text")
	btreeWantKeys(t, e, "ni", "1", "10", "2")
	run(t, e, "CREATE SCHEMA moved")
	run(t, e, "ALTER TABLE renamed SET SCHEMA moved")
	index, _ := btreeRoot(t, e, "moved.ni")
	if e.catalog.state.Get(index.Namespace()).Name() != "moved" {
		t.Fatal(index)
	}
	run(t, e, "ALTER TABLE moved.renamed DROP s")
	if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: index.Namespace(), Name: "si"})) != 0 {
		t.Fatal("indexed column left orphan")
	}
	old := e.storage.IndexRoots[index.Address()]
	if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	_, fresh := btreeRoot(t, e, "moved.ni")
	if fresh == old || fresh.Entries[0].Row == old.Entries[0].Row {
		t.Fatal("replacement retained source identities")
	}
	run(t, e, "DROP TABLE moved.renamed RESTRICT")
	if len(e.storage.IndexRoots) != 0 {
		t.Fatal("orphan roots", e.storage.IndexRoots)
	}
	run(t, e, "CREATE TABLE moved.ni(n INT)")
}
func TestBtreeIndexEmptyNullRepeatedAndCancellation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(n INT)")
	run(t, e, "CREATE INDEX empty ON t(n)")
	_, root := btreeRoot(t, e, "empty")
	if len(root.Entries) != 0 {
		t.Fatal(root)
	}
	run(t, e, "INSERT INTO t VALUES(NULL),(NULL)")
	btreeWantKeys(t, e, "empty", "NULL", "NULL")
	keys := strings.TrimSuffix(strings.Repeat("n,", 32), ",")
	run(t, e, "CREATE INDEX repeated ON t("+keys+")")
	index, root := btreeRoot(t, e, "repeated")
	if len(index.Columns) != 32 || len(root.Entries) != 2 {
		t.Fatal(index, root)
	}
	run(t, e, "CREATE TABLE build(n INT)")
	run(t, e, "INSERT INTO build VALUES"+strings.TrimSuffix(strings.Repeat("(7),", 128), ","))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 25}
	_, err = e.Execute(op, statement(t, "CREATE INDEX canceled ON build(n)"), 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "canceled"})) != 0 {
		t.Fatal("canceled definition leaked")
	}
}

func TestBtreeIndexForeignReplacement(t *testing.T) {
	source, target := New(), New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(1),(2),(NULL)", "CREATE INDEX idx ON t(n)"} {
		run(t, source, q)
	}
	_, sourceRoot := btreeRoot(t, source, "idx")
	run(t, target, "CREATE TABLE old(n INT)")
	run(t, target, "INSERT INTO old VALUES(99),(99),(99),(99),(99)")
	epoch := target.CatalogRead().Epoch()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	destination := btreeWantKeys(t, target, "idx", "1", "2", "2", "NULL")
	if target.CatalogRead().Epoch() == epoch || destination == sourceRoot {
		t.Fatal("replacement reused source authority/root")
	}
	sourceRows := make(map[ids.RowID]bool)
	for _, entry := range sourceRoot.Entries {
		sourceRows[entry.Row] = true
	}
	for _, entry := range destination.Entries {
		if sourceRows[entry.Row] {
			t.Fatal("destination did not remint identities", entry)
		}
	}
	run(t, target, "INSERT INTO t VALUES(0)")
	btreeWantKeys(t, target, "idx", "0", "1", "2", "2", "NULL")
	btreeWantKeys(t, source, "idx", "1", "2", "2", "NULL")
}

func TestBtreeIndexSchemaDropClosure(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE SCHEMA scoped", "CREATE TABLE scoped.t(n INT)", "INSERT INTO scoped.t VALUES(2),(2),(NULL)", "CREATE INDEX idx ON scoped.t(n)"} {
		run(t, e, q)
	}
	_, before := btreeRoot(t, e, "scoped.idx")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "DROP SCHEMA scoped RESTRICT"), 0, nil)
	cleanup()
	if err == nil {
		t.Fatal("nonempty schema dropped")
	}
	_, after := btreeRoot(t, e, "scoped.idx")
	if after != before {
		t.Fatal("failed RESTRICT changed root")
	}
	run(t, e, "DROP SCHEMA scoped CASCADE")
	if len(e.storage.IndexRoots) != 0 {
		t.Fatal("schema drop left roots", e.storage.IndexRoots)
	}
	for _, q := range []string{"CREATE SCHEMA scoped", "CREATE TABLE scoped.t(n INT)", "CREATE INDEX idx ON scoped.t(n)"} {
		run(t, e, q)
	}
	_, recreated := btreeRoot(t, e, "scoped.idx")
	if recreated == before {
		t.Fatal("recreation reused root")
	}
}

func TestBtreeIndexDefaultIdentityPublication(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(id INT GENERATED BY DEFAULT AS IDENTITY,n INT DEFAULT 2 CHECK(n>0))", "CREATE INDEX id_idx ON t(id)", "CREATE INDEX n_idx ON t(n)", "INSERT INTO t(n) VALUES(DEFAULT),(DEFAULT)", "INSERT INTO t DEFAULT VALUES", "INSERT INTO t(n) SELECT n FROM t WHERE id=1"} {
		run(t, e, q)
	}
	btreeWantKeys(t, e, "id_idx", "1", "2", "3", "4")
	before := btreeWantKeys(t, e, "n_idx", "2", "2", "2", "2")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "INSERT INTO t(n) VALUES(-1)"), 0, nil)
	cleanup()
	if err == nil {
		t.Fatal("failed CHECK published")
	}
	_, after := btreeRoot(t, e, "n_idx")
	if after != before {
		t.Fatal("failed default/identity publication changed root")
	}
}
