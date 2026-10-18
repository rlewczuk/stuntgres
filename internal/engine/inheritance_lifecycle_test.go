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
	table := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: name, revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), relationKind: tableRelation, index: map[string]int{}}
	if len(parents) != 0 {
		table.inheritance = &tableInheritance{parents: parents, columns: map[int32]inheritedColumn{}, checks: map[string]inheritedConstraint{}, notNull: map[int32]inheritedConstraint{}}
	}
	return table
}
func inheritanceLifecycleInstall(e *Engine, tables ...*relationDescriptor) {
	e.state = cloneCatalog(e.state)
	for _, table := range tables {
		e.state.descriptors[table.address] = table
		e.state.names[descriptorNameKey(table)] = table.address
		e.storage[table.address] = &relationStorage{}
	}
}

func TestInheritanceGraphLifecycleDiamond(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	other := inheritanceLifecycleTable(e, 20001, "other")
	left := inheritanceLifecycleTable(e, 20002, "left", p.address)
	right := inheritanceLifecycleTable(e, 20003, "right", p.address)
	leaf := inheritanceLifecycleTable(e, 20004, "leaf", left.address, right.address, other.address)
	sibling := inheritanceLifecycleTable(e, 20005, "sibling", other.address)
	inheritanceLifecycleInstall(e, p, other, left, right, leaf, sibling)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(e.domain.Ref(base.Epoch(), p.address)); err != nil {
		t.Fatal(err)
	}
	edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{p.address})
	if err != nil {
		t.Fatal(err)
	}
	var addresses []ids.ObjectAddress
	for _, edge := range edges {
		addresses = append(addresses, edge.address)
	}
	if !reflect.DeepEqual(addresses, []ids.ObjectAddress{left.address, leaf.address, right.address}) {
		t.Fatal(addresses)
	}
	if err = stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.address}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err == nil {
		t.Fatal("RESTRICT accepted descendants")
	} else {
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "2BP01" {
			t.Fatal(err)
		}
	}
	if len(stage.removals) != 1 || e.state != base.state {
		t.Fatal("RESTRICT changed catalog/closure")
	}
	if err = stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []*relationDescriptor{p, left, right, leaf} {
		if _, ok := stage.removals[table.address]; !ok {
			t.Fatal("missing descendant", table.name)
		}
	}
	for _, table := range []*relationDescriptor{other, sibling} {
		if _, ok := stage.removals[table.address]; ok {
			t.Fatal("unrelated parent/sibling removed", table.name)
		}
	}
	if e.state != base.state || leaf.inheritance.parents[2] != other.address {
		t.Fatal("source graph mutated")
	}
}

func TestInheritanceGraphLifecycleGroupAndCancel(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.address)
	inheritanceLifecycleInstall(e, p, c)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, table := range []*relationDescriptor{p, c} {
		if err := stage.Remove(e.domain.Ref(base.Epoch(), table.address)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{p.address, c.address}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := domainDependencyClosure(ctx, stage, []ids.ObjectAddress{p.address}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.state != base.state || len(stage.removals) != 2 {
		t.Fatal("cancel published changes")
	}
}

func TestInheritanceGraphDirectDependEdges(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	c := inheritanceLifecycleTable(e, 20001, "c", p.address)
	leaf := inheritanceLifecycleTable(e, 20002, "leaf", c.address)
	inheritanceLifecycleInstall(e, p, c, leaf)
	edges, err := collectPGDepend(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	var got []pgDependEdge
	for _, edge := range edges {
		if edge.dependent == leaf.address && edge.referenced.ClassID == ids.RelationClassID {
			got = append(got, edge)
		}
	}
	if !reflect.DeepEqual(got, []pgDependEdge{{dependent: leaf.address, referenced: c.address, deptype: 'n'}}) {
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
			child.address.ObjectID++
			child.name = "child"
			child.onCommit = actions.child
			child.inheritance = &tableInheritance{
				parents: []ids.ObjectAddress{parent.address},
				columns: map[int32]inheritedColumn{1: {origins: []inheritanceOrigin{{parent: parent.address, attribute: 1}}}},
				checks:  map[string]inheritedConstraint{}, notNull: map[int32]inheritedConstraint{},
			}
			e.mu.Lock()
			e.catalogChangeLocked(changes, child.address, &child)
			childStore := &relationStorage{}
			changes.storage[child.address] = catalogStorageChange{replacement: childStore, relation: &child}
			e.mu.Unlock()
			one, err := sqltype.Integer(parent.columns[0].Usage.Type, 1)
			if err != nil {
				t.Fatal(err)
			}
			two, err := sqltype.Integer(child.columns[0].Usage.Type, 2)
			if err != nil {
				t.Fatal(err)
			}
			writes := map[ids.ObjectAddress][]rowVersion{
				parent.address: {{row: 1, version: 1, values: []sqltype.Value{one}}},
				child.address:  {{row: 2, version: 2, values: []sqltype.Value{two}}},
			}
			nextWrites, _, next, err := e.prepareTemporaryCommit(op, (*ids.Domain)(nil).Tx(SessionToken{}, 0, e.state.epoch, nil, 0), writes, nil, changes)
			if err != nil {
				t.Fatal(err)
			}
			if len(writes[parent.address]) != 1 || len(writes[child.address]) != 1 || changes.descriptors[parent.address] != parent || changes.descriptors[child.address] != &child || e.state.descriptors[child.address] != nil {
				t.Fatal("commit preparation changed prior private/global roots")
			}
			if len(nextWrites[parent.address]) != 0 {
				t.Fatal("parent physical action did not clear writes")
			}
			if actions.parent == parser.OnCommitDrop {
				if next.descriptors[parent.address] != nil || next.descriptors[child.address] != nil || len(nextWrites[child.address]) != 0 {
					t.Fatal("ON COMMIT DROP omitted descendant closure")
				}
			} else if actions.child == parser.OnCommitDelete {
				if len(nextWrites[child.address]) != 0 || next.storage[child.address].replacement == childStore {
					t.Fatal("child's own DELETE action not applied")
				}
			} else if !reflect.DeepEqual(nextWrites[child.address], writes[child.address]) || next.storage[child.address].replacement != childStore || next.descriptors[child.address] != &child {
				t.Fatal("parent DELETE recursively cleared preserving child")
			}
		})
	}
}
