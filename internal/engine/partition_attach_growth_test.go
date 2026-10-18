package engine

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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

func partitionAttachUnitGrant(t *testing.T, scope *StatementScope, ref ids.ObjectRef, mode lock.Mode) {
	t.Helper()
	request, err := scope.op.session.Engine().locks.StartAcquire(scope.op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, mode, lock.Policy{NoWait: true})
	if err != nil || request != nil {
		t.Fatal("expected immediate real manager grant", request, err)
	}
}

func partitionAttachUnitQueued(t *testing.T, e *Engine, ref ids.ObjectRef, waiter *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		queued := false
		if entry != nil {
			for _, request := range entry.Pending {
				if request.Ticket == waiter && request.Mode == mode && !request.Terminal {
					queued = true
				}
			}
		}
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
			parent.partition.children = []ids.ObjectAddress{old.address}
			candidate := inheritanceUnitRelation(e, "attach_growth_candidate", 46102, "k")
			candidate.relationKind = tableRelation
			partitionAttachUnitInstall(t, e, parent, old, candidate)
			parent = e.catalog.state.MustRelation(parent.address)
			old = e.catalog.state.MustRelation(old.address)
			candidate = e.catalog.state.MustRelation(candidate.address)
			reader, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			holder, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			ref := e.catalog.domain.Ref(e.catalog.state.epoch, old.address)
			partitionAttachUnitGrant(t, holder, ref, lock.AccessExclusive)
			parentRef := ref
			parentRef.Address = parent.address
			if err := reader.acquirePartitionLock(parentRef, lock.AccessShare); err != nil {
				t.Fatal(err)
			}
			join := partitionAttachUnitWorker(t, reader, func() error { return reader.acquirePartitionDescendants(parentRef, lock.AccessShare) })
			partitionAttachUnitQueued(t, e, ref, reader.ticket, lock.AccessShare)
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			p, c := *parent, *candidate
			definition := *parent.partition
			definition.children = []ids.ObjectAddress{old.address, candidate.address}
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
			current := e.catalog.state.MustRelation(parent.address)
			if !partitionAppendCompatible(parent, current) {
				t.Fatal("same-authority append rejected")
			}
			if replacement {
				next := cloneCatalog(e.catalog.state)
				changed := *old
				changed.name = "replacement_identity"
				changed.revision++
				next.unbindName(descriptorNameKey(old))
				next.bindName(descriptorNameKey(&changed), changed.address)
				next.setDescriptor(old.address, &changed)
				e.catalog.state = next
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
			var admitted, fresh []ids.ObjectAddress
			if err := partitionPhysicalDescendants(captured, captured.MustRelation(parent.address), func(d *relationDescriptor) error { admitted = append(admitted, d.address); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := partitionPhysicalDescendants(e.CatalogRead(), current, func(d *relationDescriptor) error { fresh = append(fresh, d.address); return nil }); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(admitted, []ids.ObjectAddress{old.address}) || !slices.Equal(fresh, []ids.ObjectAddress{old.address, candidate.address}) {
				t.Fatal("admitted/fresh branch manifests", admitted, fresh)
			}
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: e.catalog.domain.Ref(ref.Epoch, candidate.address)}]
			newHold := entry != nil && entry.Holders[reader.ticket].Mode != 0
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
			target.partition.children = []ids.ObjectAddress{leaf.address}
			target.partition.defaultChild = leaf.address
			view := inheritanceUnitView(e, parent, target, leaf)
			usage, boolean := target.columns[0].Usage, testUsage(e, sqltype.Bool)
			ref := e.catalog.domain.Ref(view.Epoch(), target.address)
			ref.Address.SubID = 1
			key := columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: 0}
			cmp := func(op string, n int64) expr.Expr {
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
				return comparisonExpression{Info: expr.Info{Usage: boolean, Property: expr.Properties{RowDependent: true}}, left: key, right: literalExpression{Info: expr.Info{Usage: usage}, value: value}, predicate: predicate, operator: op}
			}
			var proof expr.Expr = logicalExpression{Info: expr.Info{Usage: boolean}, op: parser.LogicalOr, left: cmp("<", 1), right: cmp(">=", 3)}
			if mode == "candidate_proven" {
				bounds := logicalExpression{Info: expr.Info{Usage: boolean}, op: parser.LogicalAnd, left: cmp(">=", 1), right: cmp("<", 3)}
				proof = logicalExpression{Info: expr.Info{Usage: boolean}, op: parser.LogicalAnd, left: nullTestExpression{Info: expr.Info{Usage: boolean}, child: key, not: true}, right: bounds}
			}
			if mode != "default_unproven" {
				check := checkConstraint{OID: 46113, Name: "frontier_proof", expression: proof}
				var dependencies []ids.ObjectRef
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
			parent = e.catalog.state.MustRelation(parent.address)
			target = e.catalog.state.MustRelation(target.address)
			scope, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			holder, _ := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
			leafRef := e.catalog.domain.Ref(e.catalog.state.epoch, leaf.address)
			targetRef := leafRef
			targetRef.Address = target.address
			partitionAttachUnitGrant(t, holder, leafRef, lock.AccessShare)
			if err := scope.acquirePartitionLock(targetRef, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			bound := partitionAttachUnitRange(t, parent, 1, 3)
			var scans []partitionAttachScan
			join := partitionAttachUnitWorker(t, scope, func() error {
				if mode == "candidate_proven" {
					if err := scope.acquirePartitionDescendants(targetRef, lock.AccessExclusive); err != nil {
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
				partitionAttachUnitQueued(t, e, leafRef, scope.ticket, lock.AccessExclusive)
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
