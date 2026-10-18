package engine

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionAttachUnitRange(t *testing.T, parent *relationDescriptor, lower, upper int64) *partitionMembership {
	t.Helper()
	lo, err := sqltype.Integer(parent.columns[0].Usage.Type, lower)
	if err != nil {
		t.Fatal(err)
	}
	hi, err := sqltype.Integer(parent.columns[0].Usage.Type, upper)
	if err != nil {
		t.Fatal(err)
	}
	return &partitionMembership{parent: parent.address, lower: []partitionDatum{{value: lo}}, upper: []partitionDatum{{value: hi}}}
}

func partitionAttachUnitGrant(t *testing.T, scope *StatementScope, ref ObjectRef, mode relationLockMode) {
	t.Helper()
	request, err := scope.op.session.Engine().locks.startAcquire(scope.op.Context(), scope.ticket, lockResource{ObjectRef: ref}, mode, relationLockPolicy{nowait: true})
	if err != nil || request != nil {
		t.Fatal("expected immediate real manager grant", request, err)
	}
}

func partitionAttachUnitQueued(t *testing.T, e *Engine, ref ObjectRef, waiter *relationTicket, mode relationLockMode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		queued := false
		if entry != nil {
			for _, request := range entry.pending {
				if request.ticket == waiter && request.mode == mode && !request.terminal {
					queued = true
				}
			}
		}
		e.locks.mu.Unlock()
		if queued {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("expected descendant request never entered real manager queue")
		}
		runtime.Gosched()
	}
}

func partitionAttachUnitWorker(t *testing.T, scope *StatementScope, action func() error) func() error {
	t.Helper()
	done := make(chan error, 1)
	joined := false
	go func() { done <- action() }()
	t.Cleanup(func() {
		scope.op.cancel(context.Canceled)
		if !joined {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("partition worker did not terminate")
			}
		}
	})
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(6 * time.Second):
			t.Fatal("partition worker did not terminate")
			return nil
		}
	}
}

func TestPartitionAttachGrowth(t *testing.T) {
	t.Run("descendant_admission", partitionAttachUnitDescendantAdmission)
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "append_keeps_admitted_branch", true: "replacement_is_fenced"}[replacement], func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "attach_growth_parent", 46100)
			old := inheritanceUnitRelation(e, "attach_growth_old", 46101, "k")
			old.relationKind = tableRelation
			old.membership = partitionAttachUnitRange(t, parent, 20, 30)
			parent.partition.children = []ObjectAddress{old.address}
			candidate := inheritanceUnitRelation(e, "attach_growth_candidate", 46102, "k")
			candidate.relationKind = tableRelation
			partitionAttachUnitInstall(t, e, parent, old, candidate)
			parent = e.state.descriptors[parent.address].(*relationDescriptor)
			old = e.state.descriptors[old.address].(*relationDescriptor)
			candidate = e.state.descriptors[candidate.address].(*relationDescriptor)
			reader, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			holder, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			ref := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: old.address}
			partitionAttachUnitGrant(t, holder, ref, accessExclusive)
			parentRef := ref
			parentRef.Address = parent.address
			if err := reader.acquirePartitionLock(parentRef, accessShare); err != nil {
				t.Fatal(err)
			}
			join := partitionAttachUnitWorker(t, reader, func() error { return reader.acquirePartitionDescendants(parentRef, accessShare) })
			partitionAttachUnitQueued(t, e, ref, reader.ticket, accessShare)
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			p, c := *parent, *candidate
			definition := *parent.partition
			definition.children = []ObjectAddress{old.address, candidate.address}
			p.partition = &definition
			p.revision++
			c.membership = partitionAttachUnitRange(t, parent, 1, 3)
			c.revision++
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
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			current := e.state.descriptors[parent.address].(*relationDescriptor)
			if !partitionAppendCompatible(parent, current) {
				t.Fatal("same-authority append rejected")
			}
			if replacement {
				next := cloneCatalog(e.state)
				changed := *old
				changed.name = "replacement_identity"
				changed.revision++
				delete(next.names, descriptorNameKey(old))
				next.names[descriptorNameKey(&changed)] = changed.address
				next.descriptors[old.address] = &changed
				e.state = next
				// The exact descendant authority was admitted before the wait.
				reader.partitionRelations[old.address] = old
			}
			if err := holder.Finish(nil); err != nil {
				t.Fatal(err)
			}
			err = join()
			if replacement {
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("identity replacement escaped admission fence", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			captured := reader.partitionCaptureCatalog(e.CatalogRead())
			var admitted, fresh []ObjectAddress
			if err := partitionPhysicalDescendants(captured, captured.state.descriptors[parent.address].(*relationDescriptor), func(d *relationDescriptor) error { admitted = append(admitted, d.address); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := partitionPhysicalDescendants(e.CatalogRead(), current, func(d *relationDescriptor) error { fresh = append(fresh, d.address); return nil }); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(admitted, []ObjectAddress{old.address}) || !slices.Equal(fresh, []ObjectAddress{old.address, candidate.address}) {
				t.Fatal("admitted/fresh branch manifests", admitted, fresh)
			}
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: ObjectRef{owner: e.domain, Epoch: ref.Epoch, Address: candidate.address}}]
			newHold := entry != nil && entry.holders[reader.ticket].mode != 0
			e.locks.mu.Unlock()
			if newHold {
				t.Fatal("waiting reader admitted a newly attached branch")
			}
		})
	}
}

func partitionAttachUnitDescendantAdmission(t *testing.T) {
	for _, mode := range []string{"candidate_proven", "default_proven", "default_unproven"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			parent := partitionAttachUnitRoot(t, e, "attach_frontier_parent", 46110)
			target := partitionAttachUnitRoot(t, e, "attach_frontier_target", 46111)
			leaf := inheritanceUnitRelation(e, "attach_frontier_leaf", 46112, "k")
			leaf.relationKind = tableRelation
			leaf.membership = &partitionMembership{parent: target.address, defaultBound: true}
			target.partition.children = []ObjectAddress{leaf.address}
			target.partition.defaultChild = leaf.address
			view := inheritanceUnitView(e, parent, target, leaf)
			usage, boolean := target.columns[0].Usage, testUsage(e, sqltype.Bool)
			ref := ObjectRef{owner: e.domain, Epoch: view.Epoch(), Address: target.address}
			ref.Address.SubID = 1
			key := columnExpression{expressionInfo: expressionInfo{usage: usage, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: 0}
			cmp := func(op string, n int64) BoundExpr {
				operator := sqltype.CompareLess
				if op == ">=" {
					operator = sqltype.CompareGreaterEqual
				}
				predicate, err := sqltype.BindComparisonPredicate(operator, usage.Type, usage.Type, boolean.Type, usage.Collation, false)
				if err != nil {
					t.Fatal(err)
				}
				value, err := sqltype.Integer(usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return comparisonExpression{expressionInfo: expressionInfo{usage: boolean, properties: ExprProperties{RowDependent: true}}, left: key, right: literalExpression{expressionInfo: expressionInfo{usage: usage}, value: value}, predicate: predicate, operator: op}
			}
			var proof BoundExpr = logicalExpression{expressionInfo: expressionInfo{usage: boolean}, op: parser.LogicalOr, left: cmp("<", 1), right: cmp(">=", 3)}
			if mode == "candidate_proven" {
				bounds := logicalExpression{expressionInfo: expressionInfo{usage: boolean}, op: parser.LogicalAnd, left: cmp(">=", 1), right: cmp("<", 3)}
				proof = logicalExpression{expressionInfo: expressionInfo{usage: boolean}, op: parser.LogicalAnd, left: nullTestExpression{expressionInfo: expressionInfo{usage: boolean}, child: key, not: true}, right: bounds}
			}
			if mode != "default_unproven" {
				check := checkConstraint{OID: 46113, Name: "frontier_proof", expression: proof}
				var dependencies []ObjectRef
				if _, err := (scalarRenderer{owner: target, dependencies: &dependencies}).sql(context.Background(), view, proof); err != nil {
					t.Fatal(err)
				}
				for _, dependency := range dependencies {
					address := dependency.Address
					address.SubID = 0
					d, ok := view.Descriptor(address)
					if !ok {
						t.Fatal("missing real cooked dependency", address)
					}
					check.dependencies = append(check.dependencies, expressionDependency{ref: dependency, revision: d.Revision()})
				}
				target.checkConstraints = []checkConstraint{check}
			}
			partitionAttachUnitInstall(t, e, parent, target, leaf)
			parent = e.state.descriptors[parent.address].(*relationDescriptor)
			target = e.state.descriptors[target.address].(*relationDescriptor)
			scope, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			holder, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			leafRef := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: leaf.address}
			targetRef := leafRef
			targetRef.Address = target.address
			partitionAttachUnitGrant(t, holder, leafRef, accessShare)
			if err := scope.acquirePartitionLock(targetRef, accessExclusive); err != nil {
				t.Fatal(err)
			}
			bound := partitionAttachUnitRange(t, parent, 1, 3)
			var scans []partitionAttachScan
			join := partitionAttachUnitWorker(t, scope, func() error {
				if mode == "candidate_proven" {
					if err := scope.acquirePartitionDescendants(targetRef, accessExclusive); err != nil {
						return err
					}
				}
				var err error
				scans, err = scope.partitionAttachFrontier(e.CatalogRead(), parent, target, bound, mode != "candidate_proven", mode != "candidate_proven")
				return err
			})
			if mode == "default_proven" {
				// Completion while a conflicting leaf AS remains held proves the
				// sufficient DEFAULT root did not demand descendant AX admission.
				if err := join(); err != nil {
					t.Fatal(err)
				}
			} else {
				partitionAttachUnitQueued(t, e, leafRef, scope.ticket, accessExclusive)
				if err := holder.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if err := join(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "default_unproven" {
				if len(scans) != 1 || scans[0].leaf.address != leaf.address || !scans[0].exclude {
					t.Fatal("unproven DEFAULT omitted physical scan", scans)
				}
			} else if len(scans) != 0 {
				t.Fatal("sufficient proof did not elide row scan", scans)
			}
		})
	}
}
