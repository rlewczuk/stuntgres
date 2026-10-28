package partitioncmds

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func pendingReplayRelation(view catalog.Read, schema, name string) (*catalog.Relation, bool) {
	namespace := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema})
	if namespace == (ids.ObjectAddress{}) {
		return nil, false
	}
	return view.Relation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}))
}

// ImportPendingDetaches is the last Restore hook, against its isolated engine.
// It grants no SQL execution privilege: every genuine immediate edge, imported
// owner, saved strategy and executable bound qualification must already exist.
func ImportPendingDetaches(e *core.Engine, ctx context.Context, op *session.Operation, records []partition.PendingDetachReplay) error {
	if len(records) == 0 {
		return nil
	}
	validated, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	if e == nil || validated.Owner != &e.Catalog.Owner || core.EngineOf(op.Session) != e {
		return fmt.Errorf("stuntgres: foreign pending detach import authority")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || e.ImportTargets == nil {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid pending detach import authority")
	}
	auth.State.CurrentUser = bootstrap.Ref
	type edge struct{ parent, child *catalog.Relation }
	edges := make([]edge, 0, len(records))
	parents := make(map[ids.ObjectAddress]bool, len(records))
	children := make(map[ids.ObjectAddress]bool, len(records))
	invalid := func() error { return fmt.Errorf("stuntgres: invalid pending detach replay membership") }
	for _, r := range records {
		if err = ctx.Err(); err != nil {
			break
		}
		if err = r.Validate(); err != nil {
			break
		}
		parent, pok := pendingReplayRelation(auth.Catalog, r.ParentSchema, r.ParentTable)
		child, cok := pendingReplayRelation(auth.Catalog, r.ChildSchema, r.ChildTable)
		if !pok || !cok || parent.Partition == nil || (child.RelationKind != catalog.TableRelation && child.RelationKind != catalog.PartitionedRelation) ||
			child.Membership == nil || child.Membership.DetachPending || child.Membership.DefaultBound || parent.Partition.DefaultChild != (ids.ObjectAddress{}) ||
			child.Membership.Parent != parent.Address() || parents[parent.Address()] || children[child.Address()] || partition.PendingChild(auth.Catalog, parent) != nil {
			err = invalid()
			break
		}
		if err = partition.DetachMembership(parent, child); err != nil {
			err = invalid()
			break
		}
		expected := "r"
		switch parent.Partition.Strategy {
		case parser.PartitionList:
			expected = "l"
		case parser.PartitionHash:
			expected = "h"
		}
		if r.Strategy != expected || child.Membership.Strategy != parent.Partition.Strategy {
			err = invalid()
			break
		}
		if _, ok := e.ImportTargets[parent.Owner]; !ok {
			err = fmt.Errorf("stuntgres: uncaptured pending detach parent owner")
			break
		}
		if _, ok := e.ImportTargets[child.Owner]; !ok {
			err = fmt.Errorf("stuntgres: uncaptured pending detach child owner")
			break
		}
		parents[parent.Address()], children[child.Address()] = true, true
		edges = append(edges, edge{parent, child})
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	input, _ := op.Session.Settings.Presentation()
	// No flags are staged until every record's executable qualification is proven.
	for _, edge := range edges {
		if edge.child.Membership.Strategy == parser.PartitionHash {
			continue
		}
		proven, failure := partition.ConstraintProven(ctx, input, stage.Base, edge.parent, edge.child, edge.child.Membership, false)
		if failure != nil {
			return failure
		}
		if !proven {
			return fmt.Errorf("stuntgres: pending detach replay lacks valid enforced partition-bound CHECK")
		}
	}
	for _, edge := range edges {
		if err = ctx.Err(); err != nil {
			return err
		}
		e.Mu.Lock()
		revision, failure := e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if failure != nil {
			return failure
		}
		if err = partition.StagePending(stage, edge.parent, edge.child, nil, revision); err != nil {
			return err
		}
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return err
}
