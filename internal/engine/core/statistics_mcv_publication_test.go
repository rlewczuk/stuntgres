package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestStatisticsMCVPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "targetzero", "targetpositive", "definition", "layout", "storage", "provider", "hierarchy"} {
		t.Run(mode, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			enginetest.SessionSQL(t, s, "DROP STATISTICS nd_a")
			enginetest.SessionSQL(t, s, "CREATE STATISTICS nd_a ON a,b,(a+b) FROM nd_owner")
			enginetest.SessionSQL(t, s, "CREATE TABLE nd_child() INHERITS(nd_owner)")
			enginetest.SessionSQL(t, s, "INSERT INTO nd_child VALUES(2,2,2)")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner")
			a, b := ndistinctKey(t, e, "nd_a"), ndistinctKey(t, e, "nd_b")
			metadata := e.Storage.RelationMetadata[ref.Address]
			before := metadata.Statistics
			old, unselected := before.Extended[a], before.Extended[b]
			inheritedKey := a
			inheritedKey.Inherited = true
			inherited := before.Extended[inheritedKey]
			if old.MCVData == nil || old.MCVData.Rows != 2 || old.ExpressionLen() != 1 || len(old.Items) != 4 || inherited == nil || inherited.MCVData.Rows != 3 {
				t.Fatal("missing combined baseline")
			}
			store := e.Storage.Relations[ref.Address]
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(1,2,3)")
			// DML may replace its real root; statistics-only rejection must not.
			store = e.Storage.Relations[ref.Address]
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
				if mode == "storage" {
					copy := *e.Storage.Relations[ref.Address]
					e.Storage.Relations[ref.Address] = &copy
					return
				}
				next := catalog.CloneState(e.Catalog.State)
				switch mode {
				case "targetzero", "targetpositive", "definition":
					d, _ := next.Statistics(a.Definition.Address)
					copy := *d
					copy.ObjectRevision++
					if mode == "definition" {
						copy.Selection, _ = catalog.NewStatisticsSelection([]int32{2, 3}, 1, "dfme")
					} else {
						copy.TargetSet = true
						if mode == "targetpositive" {
							copy.Target = 1
						} else {
							copy.Target = 0
						}
					}
					next.SetDescriptor(copy.Address(), &copy)
				case "layout":
					rel := next.MustRelation(ref.Address)
					copy := *rel
					copy.ObjectRevision++
					copy.Columns = append([]catalog.Column(nil), rel.Columns...)
					typ, _ := e.Catalog.Current().LookupType(20)
					copy.Columns[0].Usage = typ.Usage()
					next.SetDescriptor(copy.Address(), &copy)
				case "provider":
					// The arithmetic recipe retains its input/result type
					// provider, not a catalog Operator dependency.
					changed := false
					for i := range old.Recipes.At(0).DependencyLen() {
						dependency := old.Recipes.At(0).Dependency(i)
						if d, ok := next.Get(dependency.Address).(*catalog.Type); ok {
							copy := *d
							copy.ObjectRevision = d.Revision() + 1
							next.SetDescriptor(copy.Address(), &copy)
							changed = true
							break
						}
					}
					if !changed {
						t.Fatal("fixture has no actual type provider")
					}
				case "hierarchy":
					for _, d := range next.Relations() {
						if d.Name() == "nd_child" {
							copy := *d
							copy.ObjectRevision++
							copy.Inheritance = nil
							next.SetDescriptor(copy.Address(), &copy)
						}
					}
				}
				e.Catalog.PublishLocked(next)
			}
			_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE nd_owner"), 0, nil)
			done()
			if !boundary.entered || err == nil {
				t.Fatal("changed authority accepted", boundary.entered, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Storage.RelationMetadata[ref.Address] != metadata || metadata.Statistics != before || before.Extended[a] != old || before.Extended[b] != unselected || before.Extended[inheritedKey] != inherited || old.MCVData.Rows != 2 || old.MCVData.Items[0].Count != 1 || inherited.MCVData.Rows != 3 {
				t.Fatal("partial publication or backing mutation")
			}
			if mode != "storage" && e.Storage.Relations[ref.Address] != store {
				t.Fatal("statistics rewrote row root")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("m-only capture targets and empty retention", func(t *testing.T) {
		e, s, ref := ndistinctCoreFixture(t)
		enginetest.SessionSQL(t, s, "DROP STATISTICS nd_a")
		enginetest.SessionSQL(t, s, "CREATE STATISTICS nd_a(mcv) ON a,b FROM nd_owner")
		a, b := ndistinctKey(t, e, "nd_a"), ndistinctKey(t, e, "nd_b")
		if analyzeFacts(e, ref.Address).Extended[a] != nil {
			t.Fatal("fresh definition has data")
		}
		enginetest.SessionSQL(t, s, "SET default_statistics_target=1")
		store := e.Storage.Relations[ref.Address]
		// Command admission captures settings; the plain Acquire helper used
		// for setup deliberately keeps operation defaults.
		plannerCostCommand(t, s, "ANALYZE nd_owner(a,b)")
		fact := analyzeFacts(e, ref.Address).Extended[a]
		unselected := analyzeFacts(e, ref.Address).Extended[b]
		if fact == nil || fact.MCVData.Rows != 2 || len(fact.MCVData.Items) != 1 || fact.Value.Type() != nil || fact.Dependencies.Type() != nil || e.Storage.Relations[ref.Address] != store {
			t.Fatal("m-only target or row ownership")
		}
		enginetest.SessionSQL(t, s, "ALTER STATISTICS nd_a SET STATISTICS 2")
		plannerCostCommand(t, s, "ANALYZE nd_owner(a,b)")
		fact = analyzeFacts(e, ref.Address).Extended[a]
		if len(fact.MCVData.Items) != 2 || analyzeFacts(e, ref.Address).Extended[b] != unselected {
			t.Fatal("explicit target or unselected backing")
		}
		enginetest.SessionSQL(t, s, "ALTER STATISTICS nd_a SET STATISTICS 0")
		enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(3,3,3)")
		plannerCostCommand(t, s, "ANALYZE nd_owner(a,b)")
		if analyzeFacts(e, ref.Address).Extended[a] != fact {
			t.Fatal("target zero discarded old fact")
		}
		enginetest.SessionSQL(t, s, "ALTER STATISTICS nd_a SET STATISTICS DEFAULT")
		enginetest.SessionSQL(t, s, "DELETE FROM nd_owner")
		plannerCostCommand(t, s, "ANALYZE nd_owner(a,b)")
		if analyzeFacts(e, ref.Address).Extended[a] != fact {
			t.Fatal("empty refresh discarded old fact")
		}
		enginetest.LockEmpty(t, e)
	})
}

func TestStatisticsMCVJournalOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'mcv_owner'", "ROLLBACK PREPARED 'mcv_owner'"} {
		t.Run(completion, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "DROP STATISTICS nd_a")
			enginetest.SessionSQL(t, s, "CREATE STATISTICS nd_a ON a,b,(a+b) FROM nd_owner")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner")
			a, b := ndistinctKey(t, e, "nd_a"), ndistinctKey(t, e, "nd_b")
			before := analyzeFacts(e, ref.Address)
			old := before.Extended[a]
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(1,2,3)")
			store := e.Storage.Relations[ref.Address]
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,b)")
			journal := s.Transactions.Catalog
			selected := journal.Statistics[ref.Address].Extended[a]
			if selected == nil || selected == old || selected.MCVData.Rows != 3 || selected.ExpressionLen() != 1 || len(selected.Items) != 4 || len(selected.DependencyItems) == 0 || analyzeFacts(e, ref.Address) != before || journal.Clone().Statistics[ref.Address].Extended[a] != selected || e.Storage.Relations[ref.Address] != store {
				t.Fatal("private combined ownership lost")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT mcv_mark")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,c)")
			enginetest.SessionSQL(t, s, "ROLLBACK TO mcv_mark")
			if s.Transactions.Catalog.Statistics[ref.Address].Extended[a] != selected {
				t.Fatal("savepoint replaced backing")
			}
			enginetest.SessionSQL(t, peer, "ANALYZE nd_owner(a,c)")
			newB := analyzeFacts(e, ref.Address).Extended[b]
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'mcv_owner'")
				entry := core.EnginePreparedRef(e).Lookup("mcv_owner")
				if entry == nil || entry.Catalog.Statistics[ref.Address].Extended[a] != selected {
					t.Fatal("prepared owner lost fact")
				}
				enginetest.SessionSQL(t, peer, completion)
			} else {
				enginetest.SessionSQL(t, s, completion)
			}
			want := old
			if strings.HasPrefix(completion, "COMMIT") {
				want = selected
			}
			facts := analyzeFacts(e, ref.Address)
			if facts.Extended[a] != want || facts.Extended[b] != newB || old.MCVData.Rows != 2 || old.MCVData.Items[0].Count != 1 || facts.Extended[a].MCVData == nil {
				t.Fatal("completion mutated combined/unselected backing")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
