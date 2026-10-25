package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercise UPDATE's actual target admission, mutation manifest and routing
// capture across a real descendant wait. The catalog-stage publication is the
// same append-compatible attachment interleaving as the attachment unit family.
func partitionRoutingCaptureTopologyGrowth(t *testing.T) {
	for _, fence := range []string{"append", "descriptor_replacement", "storage_replacement"} {
		t.Run(fence, func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "routing_growth_parent", 46200)
			old := inheritanceUnitRelation(e, "routing_growth_old", 46201, "k")
			old.RelationKind = catalog.TableRelation
			old.Membership = partitionAttachUnitRange(t, parent, 20, 30)
			parent.Partition.Children = []ids.ObjectAddress{old.Address()}
			candidate := inheritanceUnitRelation(e, "routing_growth_candidate", 46202, "k")
			candidate.RelationKind = catalog.TableRelation
			partitionAttachUnitInstall(t, e, parent, old, candidate)
			parent = e.Catalog.State.MustRelation(parent.Address())
			old = e.Catalog.State.MustRelation(old.Address())
			candidate = e.Catalog.State.MustRelation(candidate.Address())
			value := func(n int64) sqltype.Value {
				v, err := sqltype.Integer(parent.Columns[0].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			e.Mu.Lock()
			e.Storage.Relations[old.Address()].Versions = []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(25)}}}
			e.Storage.Relations[candidate.Address()].Versions = []storage.RowVersion{{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(2)}}}
			e.Storage.SetFrontierLocked(2, 2)
			e.Visibility.SetCommitSeqLocked(1)
			e.Mu.Unlock()
			reader, op := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			holder, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			syntax := statement(t, "UPDATE routing_growth_parent SET k=2").(*parser.Update)
			if err := reader.Gate(syntax); err != nil {
				t.Fatal(err)
			}
			reader.Statement = syntax
			oldRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), old.Address())
			parentRef := e.Catalog.Domain.Ref(oldRef.Epoch, parent.Address())
			candidateRef := e.Catalog.Domain.Ref(oldRef.Epoch, candidate.Address())
			partitionAttachUnitGrant(t, holder, oldRef, lock.AccessExclusive)
			join := partitionAttachUnitWorker(t, reader, func() error {
				if err := ScopeAcquireRelationTarget(reader, syntax, syntax, true, false); err != nil {
					return err
				}
				return ScopeAcquireInheritedMutationTargets(reader, syntax)
			})
			partitionAttachUnitQueued(t, e, oldRef, reader.LockTicket, lock.RowExclusive)
			if !e.Locks.Holds(reader.LockTicket, lock.Resource{ObjectRef: parentRef}, lock.RowExclusive) {
				t.Fatal("UPDATE did not admit its named parent before descendant wait")
			}
			e.Mu.Lock()
			admitted := reader.PartitionRelations[parent.Address()]
			e.Mu.Unlock()
			if admitted != parent {
				t.Fatal("UPDATE failed to retain pre-wait parent topology")
			}

			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			p, c := *parent, *candidate
			definition := *parent.Partition
			definition.Children = []ids.ObjectAddress{old.Address(), candidate.Address()}
			p.Partition, p.ObjectRevision = &definition, p.ObjectRevision+1
			c.Membership = partitionAttachUnitRange(t, parent, 1, 3)
			c.ObjectRevision++
			if err := stage.Replace(&p); err != nil {
				t.Fatal(err)
			}
			if err := stage.Replace(&c); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			current := e.Catalog.State.MustRelation(parent.Address())
			if !partition.AppendCompatible(parent, current) || !slices.Equal(current.Partition.Children, []ids.ObjectAddress{old.Address(), candidate.Address()}) {
				t.Fatal("fixture did not publish append-compatible topology growth")
			}
			if err := holder.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if err := join(); err != nil {
				t.Fatal(err)
			}
			if len(reader.InheritedTargets) != 1 || reader.InheritedTargets[0].Ref != oldRef {
				t.Fatal("UPDATE adopted post-admission mutation targets", reader.InheritedTargets)
			}
			auth, err := OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			reader.RoutineAuth = auth
			binding := analyze.Binding{Ref: parentRef, Relation: current}
			e.Mu.Lock()
			err = ScopeCapturePartitionInsertLocked(reader, e, auth, current)
			refs := reader.Command.TargetRefs(parentRef)
			var source *exec.Source
			if err == nil {
				data, captureErr := reader.AcquireDataViewsLocked(e, refs)
				err = captureErr
				if err == nil {
					source, err = reader.Command.CaptureSource(op.Context(), data, binding)
				}
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if !slices.Equal(refs, []ids.ObjectRef{oldRef}) || len(source.Inheritance) != 1 || source.Inheritance[0].Leaf.Ref != oldRef || source.Count() != 1 {
				t.Fatal("UPDATE source manifest broadened after descendant wait", refs, source.Inheritance, source.Count())
			}
			physical := dml.MutationPhysicalRow(source, 0, binding)
			if physical.Version.Row != 1 || len(physical.Version.Values) != 1 || physical.Version.Values[0] != value(25) || source.View.Data().GenerationAt(candidate.Address()) != nil {
				t.Fatal("post-admission branch entered UPDATE source view", physical)
			}
			routing := reader.PartitionInsert
			if routing.Target != parent || !slices.Equal(routing.Catalog.MustRelation(parent.Address()).Partition.Children, []ids.ObjectAddress{old.Address()}) {
				t.Fatal("UPDATE routing did not retain the source manifest's admitted topology")
			}
			input, output := reader.WriteHost().Environments()
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: routing.Catalog, Host: reader.WriteHost(), Input: input, Output: output}}
			destination, err := routing.Route(&evaluation, []sqltype.Value{value(25)}, input, output)
			if err != nil || destination.Address() != old.Address() {
				t.Fatal("admitted destination was lost", destination, err)
			}
			destination, err = routing.Route(&evaluation, []sqltype.Value{value(2)}, input, output)
			var diagnostic *sqlstate.Error
			if destination != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Message != `no partition of relation "routing_growth_parent" found for row` || diagnostic.Schema != "public" || diagnostic.Table != parent.Name() {
				t.Fatal("UPDATE adopted newly attached destination", destination, err)
			}
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: candidateRef}]
			if entry != nil && entry.Holders[reader.LockTicket].Mode != 0 {
				t.Fatal("UPDATE manufactured a mutation grant for the new branch")
			}
			if reader.PartitionRelations[candidate.Address()] != nil {
				t.Fatal("UPDATE silently admitted the new branch descriptor")
			}
			if err := partitionRoutingGrowthFences(reader, e, old, physical, fence); err != nil {
				t.Fatal(err)
			}
			var finishCause error
			if fence != "append" {
				finishCause = ids.ErrCatalogReplaced
			}
			if err := reader.Finish(finishCause); !errors.Is(err, finishCause) {
				t.Fatal(err)
			}
		})
	}
}

func partitionRoutingGrowthFences(s *StatementScope, e *Engine, old *catalog.Relation, physical exec.QueryPhysicalRow, fence string) error {
	if fence == "append" {
		e.Mu.Lock()
		err := s.ValidateLockedRelation(e)
		e.Mu.Unlock()
		return err
	}
	if fence == "descriptor_replacement" {
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		changed := *old
		changed.ObjectName = "routing_growth_replaced"
		changed.ObjectRevision++
		if err := stage.Replace(&changed); err != nil {
			return err
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			return err
		}
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
			return err
		}
	} else {
		e.Mu.Lock()
		store := *e.Storage.Relations[old.Address()]
		store.Versions = slices.Clone(store.Versions)
		e.Storage.InstallRelationLocked(old.Address(), &store)
		e.Mu.Unlock()
	}
	e.Mu.Lock()
	err := s.ValidateLockedRelation(e)
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		return errors.New("UPDATE admission accepted a genuine descriptor/storage replacement")
	}
	_, err = s.WriteHost().LatestRow(dml.RowRequest{Kind: dml.RowUpdate, Ref: physical.Leaf.Ref, Row: physical.Version.Row, Storage: physical.Leaf.Storage, Hint: physical.Hint})
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		return errors.New("UPDATE latest-row read accepted a replaced captured target")
	}
	return nil
}
