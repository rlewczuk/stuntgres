package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestAlterDropColumnCommentsStableAttributes(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)", "COMMENT ON TABLE t IS 'table keep'", "COMMENT ON COLUMN t.b IS 'removed comment'", "COMMENT ON COLUMN t.c IS 'survivor comment'"} {
		run(t, e, q)
	}
	run(t, e, "INSERT INTO t VALUES(1,'one',9),(2,'two',20)")
	ref := roleTableRef(t, e, "t")
	oldView := e.CatalogRead()
	old := e.catalog.state.MustRelation(ref.Address)
	run(t, e, "ALTER TABLE t DROP b")
	next := e.catalog.state.MustRelation(ref.Address)
	if next.Comment != "table keep" || !reflect.DeepEqual(next.Attributes, []int32{1, 3}) || !reflect.DeepEqual(next.ColumnComments, map[int32]string{3: "survivor comment"}) || !reflect.DeepEqual(old.ColumnComments, map[int32]string{2: "removed comment", 3: "survivor comment"}) {
		t.Fatal(old, next)
	}
	for _, sub := range []int32{2, 3} {
		column := ref
		column.Address.SubID = sub
		text, present, err := e.Comment(oldView, column)
		want := map[int32]string{2: "removed comment", 3: "survivor comment"}[sub]
		if err != nil || !present || text != want {
			t.Fatal(text, present, err)
		}
	}
	live := ref
	live.Address.SubID = 3
	if text, present, err := e.Comment(e.CatalogRead(), live); err != nil || !present || text != "survivor comment" {
		t.Fatal(text, present, err)
	}
	dead := ref
	dead.Address.SubID = 2
	if _, _, err := e.Comment(e.CatalogRead(), dead); err == nil {
		t.Fatal("dead attribute readable")
	}
	invalid := *next
	invalid.ObjectRevision++
	invalid.ColumnComments = map[int32]string{2: "invalid dropped payload", 3: "survivor comment"}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || err.Error() != "stuntgres: invalid relation column comment" {
		t.Fatal(err)
	}
	if e.catalog.state.Get(ref.Address) != next {
		t.Fatal("invalid stage changed metadata")
	}
	root := e.storage[ref.Address]
	run(t, e, "ALTER TABLE t DROP IF EXISTS missing")
	if e.catalog.state.Get(ref.Address) != next || e.storage[ref.Address] != root || !reflect.DeepEqual(next.ColumnComments, map[int32]string{3: "survivor comment"}) {
		t.Fatal("missing skip changed comments, descriptor or rows")
	}
	run(t, e, "ALTER TABLE t DROP a")
	last := e.catalog.state.MustRelation(ref.Address)
	if last.Comment != "table keep" || !reflect.DeepEqual(last.Attributes, []int32{3}) || !reflect.DeepEqual(last.ColumnComments, map[int32]string{3: "survivor comment"}) {
		t.Fatal("last survivor comment lost", last)
	}
	run(t, e, "ALTER TABLE t DROP c")
	empty := e.catalog.state.MustRelation(ref.Address)
	if empty.Comment != "table keep" || len(empty.ColumnComments) != 0 || len(empty.Columns) != 0 || len(empty.Attributes) != 0 {
		t.Fatal("final column comment retained or table comment lost", empty)
	}
	if rows := tuples(run(t, e, "SELECT * FROM t")); len(rows) != 2 || len(rows[0]) != 0 || len(rows[1]) != 0 {
		t.Fatal("empty-width rows lost", rows)
	}
	if !reflect.DeepEqual(last.ColumnComments, map[int32]string{3: "survivor comment"}) {
		t.Fatal("final drop mutated old comment payload")
	}
	run(t, e, "DROP TABLE t")
	run(t, e, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
	replacement := e.catalog.state.MustRelation(roleTableRef(t, e, "t").Address)
	if replacement.Comment != "" || len(replacement.ColumnComments) != 0 {
		t.Fatal("name recreation inherited comments", replacement)
	}
}

func TestAlterDropColumnRevisionFrontier(t *testing.T) {
	for _, drops := range []int{1, 2} {
		for _, ahead := range []bool{false, true} {
			name := map[int]string{1: "one-drop", 2: "two-drops"}[drops] + "/" + map[bool]string{false: "local", true: "allocator-ahead"}[ahead]
			t.Run(name, func(t *testing.T) {
				e := New()
				run(t, e, "CREATE TABLE t(a INT,b INT,c INT,d INT)")
				ref := roleTableRef(t, e, "t")
				if ahead {
					run(t, e, "CREATE TABLE other(a INT)")
					for i := 0; i < 4; i++ {
						run(t, e, "COMMENT ON TABLE other IS 'advance unrelated allocator'")
					}
				}
				transition := func(q string) {
					t.Helper()
					old := e.catalog.state.MustRelation(ref.Address)
					revision, columns, table, column := old.Revision(), len(old.Columns), old.Comment, old.ColumnComments[3]
					frontier := e.catalog.nextRevision
					run(t, e, q)
					next := e.catalog.state.MustRelation(ref.Address)
					if roleTableRef(t, e, "t") != ref || next == old || next.Revision() <= revision {
						t.Errorf("%s did not publish a fresh same-identity revision: old=%d new=%d", q, revision, next.Revision())
					}
					want := frontier
					if next.Revision() >= want {
						want = next.Revision() + 1
					}
					if e.catalog.nextRevision != want {
						t.Errorf("%s allocator=%d want=%d installed=%d", q, e.catalog.nextRevision, want, next.Revision())
					}
					if old.Revision() != revision || len(old.Columns) != columns || old.Comment != table || old.ColumnComments[3] != column {
						t.Error("retained descriptor mutated")
					}
				}
				transition("ALTER TABLE t DROP b")
				if drops == 2 {
					transition("ALTER TABLE t DROP d")
				}
				transition("COMMENT ON TABLE t IS 'fresh table'")
				transition("COMMENT ON COLUMN t.c IS 'fresh survivor'")
			})
		}
	}
}

func TestAlterDropColumnRevisionFrontierFailures(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.Get(ref.Address)
	root := e.storage[ref.Address]
	frontier := e.catalog.nextRevision
	unchanged := func() {
		t.Helper()
		if e.catalog.nextRevision != frontier || e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != root {
			t.Fatal("failed/canceled/skip changed frontier or publication")
		}
	}
	for _, q := range []string{"ALTER TABLE t DROP missing", "ALTER TABLE t DROP ctid"} {
		if _, err := testExecute(e, context.Background(), statement(t, q), nil); err == nil {
			t.Fatal("failure accepted", q)
		}
		unchanged()
	}
	run(t, e, "ALTER TABLE t DROP IF EXISTS missing")
	unchanged()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := e.catalog.Providers[ids.RelationClassID]
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{e.catalog.Providers[ids.RelationClassID], cancel}
	if _, err := testExecute(e, ctx, statement(t, "ALTER TABLE t DROP b"), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	e.catalog.Providers[ids.RelationClassID] = provider
	candidate := *old.(*catalog.Relation)
	candidate.ObjectRevision += 100
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&candidate); err != nil {
		t.Fatal(err)
	}
	publishContext, stop := context.WithCancel(context.Background())
	defer stop()
	prepared, err := e.ValidateCatalogStage(publishContext, stage)
	if err != nil {
		t.Fatal(err)
	}
	unchanged()
	stop()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
}

func TestAlterDropColumnRevisionFrontierTerminal(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	terminal := *old
	terminal.ObjectRevision = ids.DefinitionRevision(^uint64(0)) - 1
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&terminal); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	run(t, e, "ALTER TABLE t DROP b")
	maximum := e.catalog.state.MustRelation(ref.Address)
	root := e.storage[ref.Address]
	limit := ids.DefinitionRevision(^uint64(0))
	if maximum.Revision() != limit || e.catalog.nextRevision != limit {
		t.Errorf("terminal publication revision=%d allocator=%d want=%d", maximum.Revision(), e.catalog.nextRevision, limit)
	}
	for _, tc := range []struct{ q, message string }{{"COMMENT ON TABLE t IS 'unavailable'", "stuntgres: catalog definition revision exhausted"}, {"COMMENT ON COLUMN t.a IS 'unavailable'", "stuntgres: catalog definition revision exhausted"}, {"ALTER TABLE t DROP a", "stuntgres: definition revision exhausted"}} {
		_, err := testExecute(e, context.Background(), statement(t, tc.q), nil)
		if err == nil || err.Error() != tc.message {
			t.Errorf("%s: got %v want %s", tc.q, err, tc.message)
		}
		if e.catalog.state.Get(ref.Address) != maximum || e.storage[ref.Address] != root || e.catalog.nextRevision != limit {
			t.Errorf("%s changed terminal state", tc.q)
		}
	}
	if terminal.Revision() != limit-1 || len(terminal.Columns) != 2 {
		t.Fatal("retained terminal predecessor mutated")
	}
}
