package indexcmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Inspect every tuple component, not merely the leading ordering key.
func expressionRootTuples(t *testing.T, root *storage.IndexRoot) [][]string {
	t.Helper()
	btreeKeys(t, root)
	tuples := make([][]string, len(root.Entries))
	for i, entry := range root.Entries {
		tuples[i] = make([]string, len(entry.Keys))
		for j, key := range entry.Keys {
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

func expressionWantRoot(t *testing.T, e *engine.Engine, name string, want [][]string) *storage.IndexRoot {
	t.Helper()
	index, root := btreeRoot(t, e, name)
	if got := expressionRootTuples(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	store := e.Storage.Relations[index.SQLIndex.Parent]
	if store == nil {
		t.Fatal("missing real parent storage")
	}
	live := make(map[ids.VersionID]ids.RowID)
	for _, row := range store.Versions {
		if visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, nil) {
			live[row.Version] = row.Row
		}
	}
	if len(live) != len(root.Entries) {
		t.Fatal("index membership differs from live parent", live, root.Entries)
	}
	for _, entry := range root.Entries {
		if live[entry.Version] != entry.Row {
			t.Fatal("index membership has no matching live row version", entry)
		}
	}
	return root
}

func TestExpressionIndexSQLLifecycleRoots(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE t(extra INT,n INT,s TEXT)", "INSERT INTO t VALUES(0,1,'Alpha'),(0,5,'ALPHA'),(0,2,'Beta'),(0,NULL,NULL)", "CREATE INDEX ix ON t(lower(s),n DESC NULLS LAST,(n+1),lower(s))"} {
		enginetest.Run(t, e, q)
	}
	before := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	old := expressionWantRoot(t, e, "ix", before)
	enginetest.Run(t, e, "INSERT INTO t VALUES(0,3,'AlPhA')")
	after := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	current := expressionWantRoot(t, e, "ix", after)
	if current == old || !reflect.DeepEqual(expressionRootTuples(t, old), before) {
		t.Fatal("INSERT mutated its retained predecessor")
	}
	enginetest.Run(t, e, "ALTER TABLE t DROP extra")
	expressionWantRoot(t, e, "ix", after)
	enginetest.Run(t, e, "INSERT INTO t VALUES(4,'BETA')")
	compacted := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "4", "5", "beta"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	expressionWantRoot(t, e, "ix", compacted)
	original, _ := btreeRoot(t, e, "ix")
	enginetest.Run(t, e, "ALTER TABLE t ALTER n TYPE BIGINT USING n::bigint")
	widened := expressionWantRoot(t, e, "ix", compacted)
	index, _ := btreeRoot(t, e, "ix")
	if index.Columns[1].Usage.Type.ID() != 20 || index.Columns[2].Usage.Type.ID() != 20 || original.Columns[1].Usage.Type.ID() != 23 || original.Columns[2].Usage.Type.ID() != 23 {
		t.Fatal("TYPE lost final usages or mutated predecessor", original.Columns, index.Columns)
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES(10,'Beta')")
	final := [][]string{{"alpha", "5", "6", "alpha"}, {"alpha", "3", "4", "alpha"}, {"alpha", "1", "2", "alpha"}, {"beta", "10", "11", "beta"}, {"beta", "4", "5", "beta"}, {"beta", "2", "3", "beta"}, {"NULL", "NULL", "NULL", "NULL"}}
	expressionWantRoot(t, e, "ix", final)
	if !reflect.DeepEqual(expressionRootTuples(t, widened), compacted) {
		t.Fatal("post-TYPE INSERT mutated retained root")
	}
}

func TestExpressionIndexSQLErrorAndForeignReplacementRoots(t *testing.T) {
	source, target := engine.New(), engine.New()
	// Two source schemas match the destination's actual TEMP namespace pair;
	// the persistent table must collide with its retained sequence.
	enginetest.Run(t, source, "CREATE SCHEMA portable")
	enginetest.Run(t, source, "CREATE SCHEMA collision_padding")
	session := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, session, "CREATE TEMP SEQUENCE retained START 40 CACHE 5")
	_, description := sequencePreparedDescription(t, target, session, "SELECT nextval('retained')")
	temporary := description.Sequences.Constants[0].Ref.Address
	sequenceScalar(t, session, "SELECT nextval('retained')", 40)
	descriptor := target.Catalog.State.Get(temporary)
	generator, memory := target.Storage.Sequences[temporary], engine.PayloadOf(session).Sequences.Memory(temporary)
	generatorState, memoryState := *generator, *memory
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(2),(NULL)", "CREATE INDEX ix ON t((10/n),(n+1),(10/n))"} {
		enginetest.Run(t, source, q)
	}
	want := [][]string{{"5", "3", "5"}, {"5", "3", "5"}, {"NULL", "NULL", "NULL"}}
	old := expressionWantRoot(t, source, "ix", want)
	_, err := enginetest.Execute(source, context.Background(), enginetest.Statement(t, "INSERT INTO t VALUES(5),(0),(10)"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}) {
		t.Fatal("late batch evaluation diagnostic", err)
	}
	if current := expressionWantRoot(t, source, "ix", want); current != old {
		t.Fatal("failed evaluation replaced predecessor")
	}
	sourceIndex, _ := btreeRoot(t, source, "ix")
	if sourceIndex.SQLIndex.Parent != temporary {
		t.Fatal("fixture did not establish a retained persistent/TEMP address collision", sourceIndex.SQLIndex.Parent, temporary)
	}
	enginetest.Run(t, target, "CREATE TABLE unrelated(n INT)")
	enginetest.Run(t, target, "INSERT INTO unrelated VALUES(99),(99),(99),(99),(99)")
	epoch := target.CatalogRead().Epoch()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if target.Catalog.State.Get(temporary) != descriptor || target.Storage.Sequences[temporary] != generator || engine.PayloadOf(session).Sequences.Memory(temporary) != memory || *generator != generatorState || *memory != memoryState {
		t.Fatal("derived replacement changed retained TEMP descriptor/cache")
	}
	sequenceScalar(t, session, "SELECT nextval('retained')", 41)
	fresh := expressionWantRoot(t, target, "ix", want)
	if fresh == old || target.CatalogRead().Epoch() == epoch {
		t.Fatal("replacement reused root/catalog authority")
	}
	identities := make(map[ids.RowID]bool)
	for _, entry := range old.Entries {
		identities[entry.Row] = true
	}
	for _, entry := range fresh.Entries {
		if identities[entry.Row] {
			t.Fatal("derived replacement retained source row identity", entry)
		}
	}
	enginetest.Run(t, target, "INSERT INTO t VALUES(10)")
	expressionWantRoot(t, target, "ix", [][]string{{"1", "11", "1"}, {"5", "3", "5"}, {"5", "3", "5"}, {"NULL", "NULL", "NULL"}})
	if current := expressionWantRoot(t, source, "ix", want); current != old {
		t.Fatal("destination write changed source authority")
	}
	index, _ := btreeRoot(t, target, "ix")
	if index.SQLIndex.Parent == temporary {
		t.Fatal("replacement did not remap collided parent dependency")
	}
	for _, key := range index.SQLIndex.Keys {
		if key.Derived == nil {
			t.Fatal("replacement lost derived definition")
		}
		parentDependency := false
		for _, dependency := range key.Derived.Dependencies {
			if dependency.Domain() != target.Catalog.Domain || dependency.Epoch != target.CatalogRead().Epoch() {
				t.Fatal("replacement retained source dependency authority", dependency)
			}
			if dependency.Address.ClassID == ids.RelationClassID && dependency.Address.ObjectID == index.SQLIndex.Parent.ObjectID {
				parentDependency = true
			}
		}
		if !parentDependency {
			t.Fatal("replacement lost remapped parent dependency", key.Derived.Dependencies)
		}
	}
}

func sequencePreparedDescription(t *testing.T, e *engine.Engine, s *session.Session, query string) (parser.Statement, engine.StatementDescription) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := enginetest.Statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}

func sequenceScalar(t *testing.T, s *session.Session, q string, want int64) {
	t.Helper()
	r := enginetest.SessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}
