package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Inspect every tuple component, not merely the leading ordering key.
func expressionRootTuples(t *testing.T, root *logicalIndexRoot) [][]string {
	t.Helper()
	btreeKeys(t, root)
	tuples := make([][]string, len(root.entries))
	for i, entry := range root.entries {
		tuples[i] = make([]string, len(entry.keys))
		for j, key := range entry.keys {
			if key.IsNull() {
				tuples[i][j] = "NULL"
				continue
			}
			var err error
			tuples[i][j], err = key.Type().Output(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return tuples
}

func expressionWantRoot(t *testing.T, e *Engine, name string, want [][]string) *logicalIndexRoot {
	t.Helper()
	index, root := btreeRoot(t, e, name)
	if got := expressionRootTuples(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	store := e.storage[index.SQLIndex.parent]
	if store == nil {
		t.Fatal("missing real parent storage")
	}
	live := make(map[ids.VersionID]ids.RowID)
	for _, row := range store.versions {
		if liveVersion(row, ids.CommitSeq(^uint64(0)), store.ends, nil) {
			live[row.version] = row.row
		}
	}
	if len(live) != len(root.entries) {
		t.Fatal("index membership differs from live parent", live, root.entries)
	}
	for _, entry := range root.entries {
		if live[entry.version] != entry.row {
			t.Fatal("index membership has no matching live row version", entry)
		}
	}
	return root
}

func TestExpressionIndexSQLLifecycleRoots(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(extra INT,n INT,s TEXT)", "INSERT INTO t VALUES(0,1,'Alpha'),(0,5,'ALPHA'),(0,2,'Beta'),(0,NULL,NULL)", "CREATE INDEX ix ON t(lower(s),n DESC NULLS LAST,(n+1),lower(s))"} {
		run(t, e, q)
	}
	before := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	old := expressionWantRoot(t, e, "ix", before)
	run(t, e, "INSERT INTO t VALUES(0,3,'AlPhA')")
	after := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	current := expressionWantRoot(t, e, "ix", after)
	if current == old || !reflect.DeepEqual(expressionRootTuples(t, old), before) {
		t.Fatal("INSERT mutated its retained predecessor")
	}
	run(t, e, "ALTER TABLE t DROP extra")
	expressionWantRoot(t, e, "ix", after)
	run(t, e, "INSERT INTO t VALUES(4,'BETA')")
	compacted := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "4", "5", "beta"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	expressionWantRoot(t, e, "ix", compacted)
	original, _ := btreeRoot(t, e, "ix")
	run(t, e, "ALTER TABLE t ALTER n TYPE BIGINT USING n::bigint")
	widened := expressionWantRoot(t, e, "ix", compacted)
	index, _ := btreeRoot(t, e, "ix")
	if index.Columns[1].Usage.Type.ID() != 20 || index.Columns[2].Usage.Type.ID() != 20 || original.Columns[1].Usage.Type.ID() != 23 || original.Columns[2].Usage.Type.ID() != 23 {
		t.Fatal("TYPE lost final usages or mutated predecessor", original.Columns, index.Columns)
	}
	run(t, e, "INSERT INTO t VALUES(10,'Beta')")
	final := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "10", "11", "beta"}, {"beta", "4", "5", "beta"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	expressionWantRoot(t, e, "ix", final)
	if !reflect.DeepEqual(expressionRootTuples(t, widened), compacted) {
		t.Fatal("post-TYPE INSERT mutated retained root")
	}
}

func TestExpressionIndexSQLErrorAndForeignReplacementRoots(t *testing.T) {
	source, target := New(), New()
	// Two source schemas match the destination's actual TEMP namespace pair;
	// the persistent table must collide with its retained sequence.
	run(t, source, "CREATE SCHEMA portable")
	run(t, source, "CREATE SCHEMA collision_padding")
	session := sessionStart(t, target, nil)
	sessionSQL(t, session, "CREATE TEMP SEQUENCE retained START 40 CACHE 5")
	_, description := sequencePreparedDescription(t, target, session, "SELECT nextval('retained')")
	temporary := description.sequences.constants[0].ref.Address
	sequenceScalar(t, session, "SELECT nextval('retained')", 40)
	descriptor := target.catalog.state.Get(temporary)
	generator, memory := target.sequences[temporary], session.sequences[temporary]
	generatorState, memoryState := *generator, *memory
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(2),(NULL)", "CREATE INDEX ix ON t((10/n),(n+1),(10/n))"} {
		run(t, source, q)
	}
	want := [][]string{{"5", "3", "5"}, {"5", "3", "5"}, {"NULL", "NULL", "NULL"}}
	old := expressionWantRoot(t, source, "ix", want)
	_, err := testExecute(source, context.Background(), statement(t, "INSERT INTO t VALUES(5),(0),(10)"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}) {
		t.Fatal("late batch evaluation diagnostic", err)
	}
	if current := expressionWantRoot(t, source, "ix", want); current != old {
		t.Fatal("failed evaluation replaced predecessor")
	}
	sourceIndex, _ := btreeRoot(t, source, "ix")
	if sourceIndex.SQLIndex.parent != temporary {
		t.Fatal("fixture did not establish a retained persistent/TEMP address collision", sourceIndex.SQLIndex.parent, temporary)
	}
	run(t, target, "CREATE TABLE unrelated(n INT)")
	run(t, target, "INSERT INTO unrelated VALUES(99),(99),(99),(99),(99)")
	epoch := target.CatalogRead().Epoch()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if target.catalog.state.Get(temporary) != descriptor || target.sequences[temporary] != generator || session.sequences[temporary] != memory || *generator != generatorState || *memory != memoryState {
		t.Fatal("derived replacement changed retained TEMP descriptor/cache")
	}
	sequenceScalar(t, session, "SELECT nextval('retained')", 41)
	fresh := expressionWantRoot(t, target, "ix", want)
	if fresh == old || target.CatalogRead().Epoch() == epoch {
		t.Fatal("replacement reused root/catalog authority")
	}
	identities := make(map[ids.RowID]bool)
	for _, entry := range old.entries {
		identities[entry.row] = true
	}
	for _, entry := range fresh.entries {
		if identities[entry.row] {
			t.Fatal("derived replacement retained source row identity", entry)
		}
	}
	run(t, target, "INSERT INTO t VALUES(10)")
	expressionWantRoot(t, target, "ix", [][]string{{"1", "11", "1"}, {"5", "3", "5"}, {"5", "3", "5"}, {"NULL", "NULL", "NULL"}})
	if current := expressionWantRoot(t, source, "ix", want); current != old {
		t.Fatal("destination write changed source authority")
	}
	index, _ := btreeRoot(t, target, "ix")
	if index.SQLIndex.parent == temporary {
		t.Fatal("replacement did not remap collided parent dependency")
	}
	for _, key := range index.SQLIndex.keys {
		if key.derived == nil {
			t.Fatal("replacement lost derived definition")
		}
		parentDependency := false
		for _, dependency := range key.derived.dependencies {
			if dependency.Domain() != target.catalog.domain || dependency.Epoch != target.CatalogRead().Epoch() {
				t.Fatal("replacement retained source dependency authority", dependency)
			}
			if dependency.Address.ClassID == ids.RelationClassID && dependency.Address.ObjectID == index.SQLIndex.parent.ObjectID {
				parentDependency = true
			}
		}
		if !parentDependency {
			t.Fatal("replacement lost remapped parent dependency", key.derived.dependencies)
		}
	}
}
