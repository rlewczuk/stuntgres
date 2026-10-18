package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Descriptor-only fixture: these tests exercise dependency/control algorithms,
// not SQL parsing/execution or public driver behavior.
func inheritanceLifecycleTable(e *Engine, id ids.OID, name string, parents ...ids.ObjectAddress) *relationDescriptor {
	table := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), RelationKind: tableRelation, Index: map[string]int{}}
	if len(parents) != 0 {
		table.Inheritance = &tableInheritance{Parents: parents, Columns: map[int32]inheritedColumn{}, Checks: map[string]inheritedConstraint{}, NotNull: map[int32]inheritedConstraint{}}
	}
	return table
}
func inheritanceLifecycleInstall(e *Engine, tables ...*relationDescriptor) {
	e.catalog.state = cloneCatalog(e.catalog.state)
	for _, table := range tables {
		e.catalog.state.setDescriptor(table.Address(), table)
		e.catalog.state.bindName(descriptorNameKey(table), table.Address())
		e.storage[table.Address()] = &relationStorage{}
	}
}

func TestInheritanceGraphLifecycleDiamond(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	other := inheritanceLifecycleTable(e, 20001, "other")
	left := inheritanceLifecycleTable(e, 20002, "left", p.Address())
	right := inheritanceLifecycleTable(e, 20003, "right", p.Address())
	leaf := inheritanceLifecycleTable(e, 20004, "leaf", left.Address(), right.Address(), other.Address())
	sibling := inheritanceLifecycleTable(e, 20005, "sibling", other.Address())
	inheritanceLifecycleInstall(e, p, other, left, right, leaf, sibling)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), p.Address())); err != nil {
		t.Fatal(err)
	}
	edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{p.Address()})
	if err != nil {
		t.Fatal(err)
	}
	var addresses []ids.ObjectAddress
	for _, edge := range edges {
		addresses = append(addresses, edge.address)
	}
	if !reflect.DeepEqual(addresses, []ids.ObjectAddress{left.Address(), leaf.Address(), right.Address()}) {
		t.Fatal(addresses)
	}
	if err = stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.Address()}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err == nil {
		t.Fatal("RESTRICT accepted descendants")
	} else {
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "2BP01" {
			t.Fatal(err)
		}
	}
	if len(stage.removals) != 1 || e.catalog.state != base.state {
		t.Fatal("RESTRICT changed catalog/closure")
	}
	if err = stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.Address()}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []*relationDescriptor{p, left, right, leaf} {
		if _, ok := stage.removals[table.Address()]; !ok {
			t.Fatal("missing descendant", table.Name())
		}
	}
	for _, table := range []*relationDescriptor{other, sibling} {
		if _, ok := stage.removals[table.Address()]; ok {
			t.Fatal("unrelated parent/sibling removed", table.Name())
		}
	}
	if e.catalog.state != base.state || leaf.Inheritance.Parents[2] != other.Address() {
		t.Fatal("source graph mutated")
	}
}

func TestInheritanceGraphLifecycleGroupAndCancel(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	inheritanceLifecycleInstall(e, p, c)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, table := range []*relationDescriptor{p, c} {
		if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), table.Address())); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.Address(), c.Address()}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := domainDependencyClosure(ctx, stage, []ids.ObjectAddress{p.Address()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.catalog.state != base.state || len(stage.removals) != 2 {
		t.Fatal("cancel published changes")
	}
}

func TestInheritanceGraphDirectDependEdges(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.Address())
	leaf := inheritanceLifecycleTable(e, 20002, "leaf", c.Address())
	inheritanceLifecycleInstall(e, p, c, leaf)
	edges, err := collectPGDepend(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	var got []pgDependEdge
	for _, edge := range edges {
		if edge.dependent == leaf.Address() && edge.referenced.ClassID == ids.RelationClassID {
			got = append(got, edge)
		}
	}
	if !reflect.DeepEqual(got, []pgDependEdge{{dependent: leaf.Address(), referenced: c.Address(), deptype: 'n'}}) {
		t.Fatal(got)
	}
}

func TestInheritanceControlsTopology(t *testing.T) {
	order, err := viewDumpOrder(context.Background(), map[dumpEventKey][]dumpEventKey{{oid: 20001}: {{oid: 20003}, {oid: 20002}}, {oid: 20002}: nil, {oid: 20003}: nil}, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{20002, 20003, 20001}) {
		t.Fatal(order, err)
	}
}

func TestInheritanceGraphOnCommitPhysicalActions(t *testing.T) {
	for _, actions := range []struct {
		name          string
		parent, child parser.OnCommitAction
	}{
		{"parent-delete-child-preserve", parser.OnCommitDelete, parser.OnCommitPreserve},
		{"parent-delete-child-delete", parser.OnCommitDelete, parser.OnCommitDelete},
		{"parent-drop-child-preserve", parser.OnCommitDrop, parser.OnCommitPreserve},
	} {
		t.Run(actions.name, func(t *testing.T) {
			e, op, changes, parent := temporaryCatalogCandidate(t, actions.parent)
			child := *parent
			child.ObjectAddress.ObjectID++
			child.ObjectName = "child"
			child.OnCommit = actions.child
			child.Inheritance = &tableInheritance{
				Parents: []ids.ObjectAddress{parent.Address()},
				Columns: map[int32]inheritedColumn{1: {Origins: []inheritanceOrigin{{Parent: parent.Address(), Attribute: 1}}}},
				Checks:  map[string]inheritedConstraint{}, NotNull: map[int32]inheritedConstraint{},
			}
			e.mu.Lock()
			e.catalogChangeLocked(changes, child.Address(), &child)
			childStore := &relationStorage{}
			changes.storage[child.Address()] = catalogStorageChange{replacement: childStore, relation: &child}
			e.mu.Unlock()
			one, err := sqltype.Integer(parent.Columns[0].Usage.Type, 1)
			if err != nil {
				t.Fatal(err)
			}
			two, err := sqltype.Integer(child.Columns[0].Usage.Type, 2)
			if err != nil {
				t.Fatal(err)
			}
			writes := map[ids.ObjectAddress][]rowVersion{
				parent.Address(): {{row: 1, version: 1, values: []sqltype.Value{one}}},
				child.Address():  {{row: 2, version: 2, values: []sqltype.Value{two}}},
			}
			nextWrites, _, next, err := e.prepareTemporaryCommit(op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.catalog.state.epoch, nil, 0), writes, nil, changes)
			if err != nil {
				t.Fatal(err)
			}
			if len(writes[parent.Address()]) != 1 || len(writes[child.Address()]) != 1 || changes.descriptors[parent.Address()] != parent || changes.descriptors[child.Address()] != &child || e.catalog.state.Get(child.Address()) != nil {
				t.Fatal("commit preparation changed prior private/global roots")
			}
			if len(nextWrites[parent.Address()]) != 0 {
				t.Fatal("parent physical action did not clear writes")
			}
			if actions.parent == parser.OnCommitDrop {
				if next.descriptors[parent.Address()] != nil || next.descriptors[child.Address()] != nil || len(nextWrites[child.Address()]) != 0 {
					t.Fatal("ON COMMIT DROP omitted descendant closure")
				}
			} else if actions.child == parser.OnCommitDelete {
				if len(nextWrites[child.Address()]) != 0 || next.storage[child.Address()].replacement == childStore {
					t.Fatal("child's own DELETE action not applied")
				}
			} else if !reflect.DeepEqual(nextWrites[child.Address()], writes[child.Address()]) || next.storage[child.Address()].replacement != childStore || next.descriptors[child.Address()] != &child {
				t.Fatal("parent DELETE recursively cleared preserving child")
			}
		})
	}
}
