package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func TestAnalyzeInheritedForeignDetachedAuthority(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, phase := range []string{"negotiate", "acquire", "import"} {
			for _, action := range []string{"cancel", "failure", "mapping", "server", "rights", "owner", "replace"} {
				t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+phase+"/"+action, func(t *testing.T) {
					f := newInheritedForeignFixture(t, 10, partition)
					f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
					enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_detached(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
					enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_detached_expression ON (a+b) FROM mixed_parent")
					f.analyze(t, false, "")
					before := f.e.Storage.RelationMetadata[f.parent.Address]
					retained := f.retained(t)
					enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
					peer := f
					peer.s = enginetest.StartSession(t, f.e, nil)
					if action == "owner" {
						enginetest.SessionSQL(t, peer.s, "CREATE ROLE mixed_replacement_owner")
						enginetest.SessionSQL(t, peer.s, "GRANT USAGE ON FOREIGN SERVER mixed_owner_server TO mixed_replacement_owner")
						enginetest.SessionSQL(t, peer.s, "CREATE USER MAPPING FOR mixed_replacement_owner SERVER mixed_owner_server")
					}
					if action == "rights" {
						for _, q := range []string{"CREATE ROLE mixed_maintainer", "GRANT MAINTAIN ON mixed_parent TO mixed_maintainer", "SET ROLE mixed_maintainer"} {
							enginetest.SessionSQL(t, f.s, q)
						}
					}
					capturedChild := f.e.CatalogRead().MustRelation(f.foreign.Address)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					entered, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					failure := errors.New("detached inherited failure")
					gate := func(active context.Context) error {
						close(entered)
						select {
						case <-release:
						case <-active.Done():
							return active.Err()
						}
						if action == "failure" {
							return failure
						}
						return active.Err()
					}
					var gateOnce sync.Once
					if phase == "import" {
						f.wrapper.importHook = func(active context.Context) error {
							var err error
							gateOnce.Do(func() { err = gate(active) })
							return err
						}
					} else {
						f.wrapper.hook = func(active context.Context, p, _ string) error {
							if p == phase {
								return gate(active)
							}
							return active.Err()
						}
					}
					done := make(chan error, 1)
					go func() { done <- f.execute(t, ctx, "ANALYZE ONLY mixed_parent", false) }()
					joined := false
					defer func() {
						cancel()
						unblock()
						if !joined {
							<-done
						}
					}()
					select {
					case <-entered:
					case err := <-done:
						joined = true
						t.Fatal("intended detached phase not reached", err)
					case <-ctx.Done():
						t.Fatal("detached gate blocked", ctx.Err())
					}
					// Real peer SQL must finish while provider/import work holds no
					// engine or replacement read lock. Use its own bounded operation.
					peerDone := enginetest.ExecuteAsync(t, f.e, peer.s, ctx, "SELECT 42")
					result, err := peerDone()
					if result.Rows != nil {
						result.Rows.Close()
					}
					if err != nil {
						t.Fatal("peer SQL blocked by detached callback", err)
					}
					var installed *storage.RelationMetadata
					switch action {
					case "cancel":
						cancel()
					case "mapping":
						if err := peer.execute(t, ctx, "ALTER USER MAPPING FOR CURRENT_USER SERVER mixed_owner_server OPTIONS(ADD user 'changed')", false); err != nil {
							t.Fatal(err)
						}
					case "server":
						if err := peer.execute(t, ctx, "ALTER SERVER mixed_owner_server OPTIONS(ADD host 'changed')", false); err != nil {
							t.Fatal(err)
						}
					case "rights":
						if err := peer.execute(t, ctx, "REVOKE MAINTAIN ON mixed_parent FROM mixed_maintainer", false); err != nil {
							t.Fatal(err)
						}
					case "owner":
						successor, ok := f.e.Roles().Lookup("mixed_replacement_owner")
						if !ok {
							t.Fatal("missing genuinely created successor owner")
						}
						// SQL ALTER correctly waits for this pass's AccessShare
						// and partition ALTER is a separate unsupported feature.
						// Use the established catalog COW authority-test seam:
						// publish a real current descriptor, never mutate the
						// retained descriptor or bypass the live lock manager.
						f.e.Mu.Lock()
						state := catalog.CloneState(f.e.Catalog.State)
						child := *state.MustRelation(f.foreign.Address)
						child.Owner = successor.Ref
						child.ObjectRevision++
						state.SetDescriptor(child.Address(), &child)
						f.e.Catalog.PublishLocked(state)
						f.e.Mu.Unlock()
						if current := f.e.CatalogRead().MustRelation(f.foreign.Address); current.Owner != successor.Ref || current == capturedChild || capturedChild.Owner == successor.Ref {
							t.Fatal("owner transition did not publish independently owned actual authority")
						}
					case "replace":
						if _, err := f.e.ReplaceCatalog(ctx, f.e, f.e.CatalogRead()); err != nil {
							t.Fatal("replacement held across detached work", err)
						}
						installed = f.e.Storage.RelationMetadata[f.parent.Address]
					}
					unblock()
					err = <-done
					joined = true
					if err == nil {
						t.Fatal("detached failure/cancellation/authority change succeeded")
					}
					if action == "failure" && !errors.Is(err, failure) {
						t.Fatal("original sentinel replaced", err)
					}
					if action == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("ordinary cancellation changed", err)
					}
					if action == "owner" {
						request := f.wrapper.negotiated[len(f.wrapper.negotiated)-1]
						if uint64(request.RoleOID) != capturedChild.Owner.ID() || request.RoleOID == 0 {
							t.Fatal("detached work did not retain actual original child-owner authority", request)
						}
						if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
							t.Fatal("owner fence was replaced by timeout/cancellation", err)
						}
					}
					if action == "replace" {
						if f.e.Storage.RelationMetadata[f.parent.Address] != installed {
							t.Fatal("rejected old pass replaced reminted current metadata")
						}
					} else {
						after := f.e.Storage.RelationMetadata[f.parent.Address]
						if partition {
							if after != before || f.e.Storage.Relations[f.parent.Address] != nil {
								t.Fatal("partition detached rejection published an unfinished pass")
							}
						} else if after.Statistics != before.Statistics || after.InheritedTuples != 40 || after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 11 {
							t.Fatal("detached rejection lost local pass or leaked inherited publication", after)
						}
					}
					if f.wrapper.active.Load() != 0 {
						t.Fatal("detached sampler worker not released")
					}
					retained()
					enginetest.LockEmpty(t, f.e)
				})
			}
		}
	}
}

func TestAnalyzeInheritedForeignRetainedTemporary(t *testing.T) {
	for _, replacement := range []string{"reset", "self_import"} {
		t.Run(replacement, func(t *testing.T) {
			f := newInheritedForeignFixture(t, 10, false)
			f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
			for _, q := range []string{"CREATE STATISTICS mixed_retained(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent", "ANALYZE ONLY mixed_parent", "CREATE TEMP TABLE mixed_kept(a integer,b integer)", "CREATE TEMP TABLE mixed_kept_child() INHERITS(mixed_kept)", "INSERT INTO mixed_kept VALUES(7,70),(7,70),(8,80)", "INSERT INTO mixed_kept_child VALUES(9,90),(9,90)", "CREATE STATISTICS pg_temp.mixed_temp_joint(ndistinct,dependencies,mcv) ON a,b FROM mixed_kept", "CREATE STATISTICS pg_temp.mixed_temp_expression ON (a+b) FROM mixed_kept", "ANALYZE ONLY mixed_kept"} {
				enginetest.SessionSQL(t, f.s, q)
			}
			var tempNameFound bool
			var tempMetadata *storage.RelationMetadata
			var tempHeap *storage.Relation
			var tempAddress = f.parent.Address
			for _, d := range f.e.CatalogRead().Descriptors() {
				r, ok := f.e.CatalogRead().Relation(d.Address())
				if !ok || r.Name() != "mixed_kept" {
					continue
				}
				if f.e.CatalogRead().MustNamespace(r.Namespace()).Temporary != f.s.SessionToken {
					t.Fatal("not genuine destination TEMP authority")
				}
				tempAddress = r.Address()
				tempMetadata = f.e.Storage.RelationMetadata[tempAddress]
				tempHeap = f.e.Storage.Relations[tempAddress]
				tempNameFound = true
			}
			if !tempNameFound || tempMetadata == nil || tempHeap == nil {
				t.Fatal("missing actual retained TEMP fixture")
			}
			tempMaintenance := tempHeap.Maintenance
			if tempMetadata.InheritedTuples != 5 || tempMaintenance.Tuples != 3 || len(tempMetadata.Statistics.Extended) != 4 {
				t.Fatal("TEMP fixture lacks genuine local/inherited scalar and extended ownership", tempMetadata)
			}
			var retainedTemp []func()
			for key, fact := range tempMetadata.Statistics.Extended {
				read := f.e.CatalogRead()
				definition, ok := read.Statistics(key.Definition.Address)
				if !ok || definition.Relation.Address != tempAddress || read.MustNamespace(definition.Namespace()).Temporary != f.s.SessionToken {
					t.Fatal("statistics definition is not genuinely destination-TEMP-owned", key)
				}
				retainedTemp = append(retainedTemp, retainMixedExtended(t, fact))
			}
			before := f.e.Storage.RelationMetadata[f.parent.Address]
			retained := f.retained(t)
			calls := len(f.wrapper.negotiated)
			f.wrapper.hook = func(context.Context, string, string) error {
				t.Fatal("native metadata replacement negotiated provider")
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := f.e.ReplaceCatalog(ctx, f.e, f.e.CatalogRead()); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled replacement succeeded", err)
			}
			if f.e.Storage.RelationMetadata[f.parent.Address] != before || f.e.Storage.RelationMetadata[tempAddress] != tempMetadata {
				t.Fatal("cancelled replacement changed metadata")
			}
			var err error
			if replacement == "reset" {
				err = engine.Reset(context.Background(), f.e)
			} else {
				_, err = f.e.ReplaceCatalog(context.Background(), f.e, f.e.CatalogRead())
			}
			if err != nil {
				t.Fatal(err)
			}
			current := f.e.Storage.RelationMetadata[tempAddress]
			if current == nil || current.Statistics == nil || f.e.Storage.Relations[tempAddress] != tempHeap || tempHeap.Maintenance != tempMaintenance {
				t.Fatal("native replacement lost retained TEMP physical or statistics authority")
			}
			header := *current
			header.Statistics = tempMetadata.Statistics
			if header != *tempMetadata {
				t.Fatal("native replacement changed genuine TEMP maintenance/count/time/cardinality")
			}
			if len(current.Statistics.Columns) != len(tempMetadata.Statistics.Columns) || len(current.Statistics.Extended) != len(tempMetadata.Statistics.Extended) {
				t.Fatal("native replacement lost retained TEMP scalar/extended flavors")
			}
			for key, fact := range tempMetadata.Statistics.Columns {
				if current.Statistics.Columns[key] != fact {
					t.Fatal("unchanged integer TEMP scalar sample backing was rebuilt", key)
				}
			}
			read := f.e.CatalogRead()
			root := read.MustRelation(tempAddress)
			for oldKey, old := range tempMetadata.Statistics.Extended {
				key := oldKey
				key.Definition = read.Domain().Ref(read.Epoch(), oldKey.Definition.Address)
				definition, ok := read.Statistics(key.Definition.Address)
				fact := current.Statistics.Extended[key]
				if !ok || key.Definition.Epoch == oldKey.Definition.Epoch || fact == nil || fact == old || fact.Relation != definition.Relation || fact.Recipes != definition.Expressions || read.MustNamespace(definition.Namespace()).Temporary != f.s.SessionToken {
					t.Fatal("TEMP extended carrier was not reowned to actual current definition", key)
				}
				if key.Inherited && !storage.StatisticsHierarchyCurrent(fact.Hierarchy, root, read) {
					t.Fatal("retained TEMP inherited carrier lost genuine hierarchy authority", key)
				}
				if len(fact.Items) != len(old.Items) || len(fact.DependencyItems) != len(old.DependencyItems) || fact.MCVData != old.MCVData || fact.ExpressionLen() != old.ExpressionLen() {
					t.Fatal("TEMP reownership rebuilt genuine joint/expression construction roots", key)
				}
				if len(old.Items) != 0 && &fact.Items[0] != &old.Items[0] || len(old.DependencyItems) != 0 && &fact.DependencyItems[0] != &old.DependencyItems[0] {
					t.Fatal("TEMP reownership copied immutable joint construction backing", key)
				}
				for i := range old.ExpressionLen() {
					if fact.Expression(i) != old.Expression(i) {
						t.Fatal("unchanged integer TEMP expression sample backing rebuilt", key, i)
					}
				}
			}
			if storage.CompatibleExtendedStatistics(current.Statistics, root, read) != current.Statistics {
				t.Fatal("current retained TEMP definition/provider authority rejected")
			}
			enginetest.TransactionRows(t, f.s, "SELECT a FROM mixed_kept ORDER BY a", 7, 7, 8, 9, 9)
			for _, check := range retainedTemp {
				check()
			}
			if f.e.Storage.RelationMetadata[f.parent.Address] != nil {
				t.Fatal("persistent mixed metadata survived native replacement/remint")
			}
			if len(f.wrapper.negotiated) != calls {
				t.Fatal("control operation reacquired foreign sample")
			}
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}
