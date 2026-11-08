package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/viewcmds"
)

func triggerCaptureAtomicTransfer(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, reason := range []string{"accepted", "primary", "inheritance", "partition", "storage", "foreign", "epoch", "source"} {
			t.Run(fmt.Sprintf("explicit_%t/%s", explicit, reason), func(t *testing.T) {
				e, s, addresses := triggerCaptureFixture(t, true)
				enginetest.SessionSQL(t, s, "CREATE TRIGGER "+captureTriggerLong+" BEFORE INSERT ON capture_root FOR EACH ROW EXECUTE FUNCTION capture_fn()")
				before := e.CatalogRead()
				if explicit {
					enginetest.SessionSQL(t, s, "BEGIN")
				}
				op, _, err := s.AcquireCommand(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished atomic capture witness"))
				scope.ProtocolSimpleQuery(1)
				stmt, _ := triggerCaptureParse(t, op, "ALTER TRIGGER "+captureTriggerLong+" ON capture_root RENAME TO capture_next")
				if err = scope.Gate(stmt); err != nil {
					t.Fatal(err)
				}
				if err = scope.AcquireRelation(stmt, true); err != nil {
					t.Fatal(err)
				}
				if err = scope.AcquirePartitionDescendants(scope.LockedRef, lock.AccessExclusive); err != nil {
					t.Fatal(err)
				}
				// Admit real independent source captures through the existing owner,
				// rather than constructing equal descriptors or adding grants by hand.
				if err = core.ScopeAcquireInheritanceSources(scope, nil, []exec.Leaf{
					{Ref: before.Domain().Ref(before.Epoch(), addresses[1])},
					{Ref: before.Domain().Ref(before.Epoch(), addresses[2])},
				}); err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.StatementAuthorizationLocked(op, stmt)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				change, err := view.PlanTrigger(viewcmds.ViewHost{TableHost: tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op}}}, auth, stmt, scope.LockedRef, scope.Input)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, s)
				stage.QuerySources = append(stage.QuerySources, change.Sources...)
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(stage)
				if scope.ReplacementRead {
					e.ReplacementMu.RUnlock()
					scope.ReplacementRead = false
				}
				if err = stage.Replace(change.Next); err != nil {
					t.Fatal(err)
				}
				prepared, err := e.ValidateCatalogStage(op.Context(), stage)
				if err != nil {
					t.Fatal(err)
				}
				for _, address := range addresses {
					if stage.Get(address) == nil || stage.Get(address) == before.Get(address) {
						t.Fatal("normalization omitted a genuinely rewritten descendant", address)
					}
				}
				inherited := core.InheritanceScopeRelations(&scope.Inheritance)
				if len(inherited) < 2 || len(scope.PartitionRelations) < 3 {
					t.Fatal("missing genuine independent captures")
				}
				unrelated := *before.MustRelation(addresses[2])
				unrelated.ObjectRevision = unrelated.Revision() + 1
				switch reason {
				case "primary":
					copy := *before.MustRelation(addresses[0])
					scope.LockedDescriptor = &copy
				case "inheritance":
					copy := *inherited[len(inherited)-1].Relation
					inherited[len(inherited)-1].Relation = &copy
				case "partition":
					scope.PartitionRelations[addresses[2]] = &unrelated
				case "storage":
					other := enginetest.RoleTableRef(t, e, "capture_other")
					inherited[len(inherited)-1].Storage = e.Storage.Relations[other.Address]
				case "foreign":
					scope.LockedRef = engine.New().Catalog.Domain.Ref(before.Epoch(), addresses[0])
				case "epoch":
					scope.LockedRef.Epoch++
				case "source":
					intervening := e.BeginCatalogStage(e.CatalogRead())
					defer e.DiscardCatalogStage(intervening)
					if err := intervening.Replace(&unrelated); err != nil {
						t.Fatal(err)
					}
					enginetest.PublishStage(t, e, intervening)
				}
				primary := scope.StatementMutation
				mutations := slices.Clone(core.ScopeMutations(scope))
				sources := slices.Clone(inherited)
				topology := maps.Clone(scope.PartitionRelations)
				committed := e.CatalogRead()
				journal := scope.Catalog
				if explicit {
					err = e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone())
				} else {
					_, err = e.PublishPreparedCatalog(op, scope, prepared, core.PublicationToken{Epoch: before.Epoch()})
				}
				if reason == "accepted" {
					if err != nil {
						t.Fatal(err)
					}
					triggerCaptureSuccessor(t, e, scope, before, addresses, "capture_next", map[ids.ObjectAddress]ids.OID{})
					installed := triggerCaptureRead(e, scope.PendingCatalog()).Get(addresses[0])
					if explicit && e.CatalogRead().Get(addresses[0]) != before.Get(addresses[0]) || !explicit && e.CatalogRead().Get(addresses[0]) != installed {
						t.Fatal("private/committed installation escaped its publication lane")
					}
					if err = scope.ProtocolNotices(func() error { triggerCaptureUnlocked(t, e, scope); op.DetachNotices(); return nil }); err != nil {
						t.Fatal(err)
					}
					if err = scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil {
					t.Fatal("foreign/stale capture was adopted")
				}
				if e.CatalogRead().State() != committed.State() || scope.Catalog != journal || scope.LockedDescriptor != primary.LockedDescriptor || scope.LockedRef != primary.LockedRef || scope.LockedStorage != primary.LockedStorage {
					t.Fatal("failed proof partially installed or advanced the primary capture", err)
				}
				for i, mutation := range core.ScopeMutations(scope) {
					if mutation.LockedDescriptor != mutations[i].LockedDescriptor || mutation.LockedStorage != mutations[i].LockedStorage || mutation.LockedRef != mutations[i].LockedRef {
						t.Fatal("partially advanced mutation")
					}
				}
				for i, source := range core.InheritanceScopeRelations(&scope.Inheritance) {
					if source.Relation != sources[i].Relation || source.Storage != sources[i].Storage || source.Ref != sources[i].Ref {
						t.Fatal("partially advanced inheritance capture")
					}
				}
				if !maps.Equal(scope.PartitionRelations, topology) {
					t.Fatal("partially advanced topology captures")
				}
				for _, address := range addresses {
					if before.Get(address) != stage.Base.Get(address) || len(stored.TriggersOf(before.MustRelation(address))) != 2 {
						t.Fatal("failed proof mutated retained predecessor")
					}
				}
				_ = scope.Finish(err)
			})
		}
	}
}
