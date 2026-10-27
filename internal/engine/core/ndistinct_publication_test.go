package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"testing"
)

func ndistinctCoreFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE nd_owner(a INT,b INT,c INT)", "CREATE STATISTICS nd_a(ndistinct) ON a,b FROM nd_owner", "CREATE STATISTICS nd_b(ndistinct) ON a,c FROM nd_owner", "INSERT INTO nd_owner VALUES(1,1,1),(2,2,2)", "ANALYZE nd_owner"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "nd_owner")
	return e, s, ref
}
func ndistinctKey(t *testing.T, e *core.Engine, name string) storage.ExtendedStatisticsKey {
	t.Helper()
	for _, raw := range e.CatalogRead().Descriptors() {
		if d, ok := raw.(*catalog.Statistics); ok && d.Name() == name {
			return storage.ExtendedStatisticsKey{Definition: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())}
		}
	}
	t.Fatal("missing definition", name)
	return storage.ExtendedStatisticsKey{}
}

func TestStatisticsNDistinctJournal(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'nd_owner'", "ROLLBACK PREPARED 'nd_owner'"} {
		t.Run(completion, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			a, b := ndistinctKey(t, e, "nd_a"), ndistinctKey(t, e, "nd_b")
			baseline := analyzeFacts(e, ref.Address)
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(3,3,3)")
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,b)")
			first := s.Transactions.Catalog
			selected := first.Statistics[ref.Address].Extended[a]
			if selected == nil || selected.Items[0].Value != 3 || len(first.Statistics[ref.Address].Extended) != 1 || analyzeFacts(e, ref.Address) != baseline {
				t.Fatal("selected extended delta leaked or missing")
			}
			clone := first.Clone()
			if clone.Statistics[ref.Address].Extended[a] != selected {
				t.Fatal("clone copied immutable fact")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT nd_mark")
			enginetest.SessionSQL(t, s, "ANALYZE nd_owner(a,c)")
			if len(s.Transactions.Catalog.Statistics[ref.Address].Extended) != 2 || len(first.Statistics[ref.Address].Extended) != 1 {
				t.Fatal("merge mutated old delta")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK TO nd_mark")
			if s.Transactions.Catalog.Statistics[ref.Address].Extended[a] != selected || len(s.Transactions.Catalog.Statistics[ref.Address].Extended) != 1 {
				t.Fatal("savepoint lost backing")
			}
			enginetest.SessionSQL(t, peer, "ANALYZE nd_owner(a,c)")
			newB := analyzeFacts(e, ref.Address).Extended[b]
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'nd_owner'")
				entry := core.EnginePreparedRef(e).Lookup("nd_owner")
				if entry == nil || entry.Catalog.Statistics[ref.Address].Extended[a] != selected {
					t.Fatal("prepared owner lost typed delta")
				}
				enginetest.SessionSQL(t, peer, completion)
			} else {
				enginetest.SessionSQL(t, s, completion)
			}
			want := baseline.Extended[a]
			if strings.HasPrefix(completion, "COMMIT") {
				want = selected
			}
			facts := analyzeFacts(e, ref.Address)
			if facts.Extended[a] != want || facts.Extended[b] != newB || baseline.Extended[a].Items[0].Value != 2 {
				t.Fatal("completion changed unselected/old immutable backing")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

type ndistinctBoundaryContext struct {
	context.Context
	entered bool
	action  func()
}

func (c *ndistinctBoundaryContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".(*NDistinctCollector).Add") {
				c.entered = true
				c.action()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestStatisticsNDistinctPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "targetzero", "definition", "storage", "layout"} {
		t.Run(mode, func(t *testing.T) {
			e, s, ref := ndistinctCoreFixture(t)
			enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(3,3,3)")
			metadata := e.Storage.RelationMetadata[ref.Address]
			before := metadata.Statistics
			a := ndistinctKey(t, e, "nd_a")
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
						copy.Selection, _ = catalog.NewStatisticsSelection([]int32{2, 3}, 0, "d")
					}
					next.SetDescriptor(copy.Address(), &copy)
					e.Catalog.PublishLocked(next)
				case "layout":
					next := catalog.CloneState(e.Catalog.State)
					old := next.MustRelation(ref.Address)
					copy := *old
					copy.ObjectRevision++
					copy.Columns = append([]catalog.Column(nil), old.Columns...)
					typ, _ := e.Catalog.Current().LookupType(20)
					copy.Columns[0].Usage = typ.Usage()
					next.SetDescriptor(copy.Address(), &copy)
					e.Catalog.PublishLocked(next)
				}
			}
			_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE nd_owner"), 0, nil)
			done()
			if !boundary.entered || err == nil {
				t.Fatal("real detached collection failed to reject authority", boundary.entered, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Storage.RelationMetadata[ref.Address] != metadata || metadata.Statistics != before || before.Extended[ndistinctKey(t, e, "nd_b")].Items[0].Value != 2 {
				t.Fatal("rejected scan partially published metadata")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
