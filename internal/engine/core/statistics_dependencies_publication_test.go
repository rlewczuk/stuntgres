package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"testing"
)

func TestStatisticsDependenciesPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "targetzero", "definition", "layout", "storage"} {
		t.Run(mode, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			enginetest.SessionSQL(t, s, "DROP STATISTICS nd_a")
			enginetest.SessionSQL(t, s, "CREATE STATISTICS nd_a(ndistinct,dependencies) ON a,b FROM nd_owner")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner")
			a, b := ndistinctKey(t, e, "nd_a"), ndistinctKey(t, e, "nd_b")
			metadata := e.Storage.RelationMetadata[ref.Address]
			before := metadata.Statistics
			old := before.Extended[a]
			if len(old.DependencyItems) != 2 || old.DependencyItems[0].Value != 1 {
				t.Fatal("missing combined baseline", old)
			}
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(1,2,3)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			boundary := &ndistinctBoundaryContext{Context: op.Ctx}
			op.Ctx = boundary
			boundary.action = func() {
				if mode == "cancel" {
					cancel()
					return
				}
				e.Mu.Lock()
				defer e.Mu.Unlock()
				switch mode {
				case "storage":
					copy := *e.Storage.Relations[ref.Address]
					e.Storage.Relations[ref.Address] = &copy
				case "targetzero", "definition":
					next := catalog.CloneState(e.Catalog.State)
					d, _ := next.Statistics(a.Definition.Address)
					copy := *d
					copy.ObjectRevision++
					if mode == "targetzero" {
						copy.TargetSet = true
						copy.Target = 0
					} else {
						copy.Selection, _ = catalog.NewStatisticsSelection([]int32{2, 3}, 0, "df")
					}
					next.SetDescriptor(copy.Address(), &copy)
					e.Catalog.PublishLocked(next)
				case "layout":
					next := catalog.CloneState(e.Catalog.State)
					rel := next.MustRelation(ref.Address)
					copy := *rel
					copy.ObjectRevision++
					copy.Columns = append([]catalog.Column(nil), rel.Columns...)
					typ, _ := e.Catalog.Current().LookupType(20)
					copy.Columns[0].Usage = typ.Usage()
					next.SetDescriptor(copy.Address(), &copy)
					e.Catalog.PublishLocked(next)
				}
			}
			_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE nd_owner"), 0, nil)
			done()
			if !boundary.entered || err == nil {
				t.Fatal("collection did not reject changed authority", boundary.entered, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Storage.RelationMetadata[ref.Address] != metadata || metadata.Statistics != before || before.Extended[a] != old || before.Extended[b].Items[0].Value != 2 || old.Items[0].Value != 2 || old.DependencyItems[0].Value != 1 {
				t.Fatal("partial combined publication or old backing mutation")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	// Real Journal deltas own a combined immutable fact through savepoint cloning
	// and transaction completion. No dependency-only side publication exists.
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			enginetest.SessionSQL(t, s, "DROP STATISTICS nd_a")
			enginetest.SessionSQL(t, s, "CREATE STATISTICS nd_a(ndistinct,dependencies) ON a,b FROM nd_owner")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner")
			a := ndistinctKey(t, e, "nd_a")
			before := analyzeFacts(e, ref.Address)
			old := before.Extended[a]
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(1,2,3)")
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,b)")
			journal := s.Transactions.Catalog
			selected := journal.Statistics[ref.Address].Extended[a]
			if selected == nil || selected == old || len(selected.DependencyItems) != 2 || selected.DependencyItems[0].Value != 1.0/3 || selected.Items[0].Value != 3 || analyzeFacts(e, ref.Address) != before || journal.Clone().Statistics[ref.Address].Extended[a] != selected {
				t.Fatal("private combined root lost ownership")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT dep_mark")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,c)")
			enginetest.SessionSQL(t, s, "ROLLBACK TO dep_mark")
			if s.Transactions.Catalog.Statistics[ref.Address].Extended[a] != selected {
				t.Fatal("savepoint replaced combined backing")
			}
			enginetest.SessionSQL(t, s, completion)
			want := old
			if completion == "COMMIT" {
				want = selected
			}
			if analyzeFacts(e, ref.Address).Extended[a] != want || old.DependencyItems[0].Value != 1 || old.Items[0].Value != 2 {
				t.Fatal("completion mutated old combined fact")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
