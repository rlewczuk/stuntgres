package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestUserOperatorDumpPartitionInterleaving(t *testing.T) {
	e := New()
	stage := e.BeginCatalogStage(e.CatalogRead())
	a, b := ids.OID(90001), ids.OID(90003)
	operators := []*catalog.Operator{
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: a}, ObjectName: "#~#", Commutator: b},
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: b}, ObjectName: "#@#", Commutator: a},
	}
	for _, op := range operators {
		op.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		op.ObjectRevision = 1
		op.Left = 23
		op.Right = 23
		op.Owner = e.roles.domain.Principal(10)
		op.Function = 65
		op.Result = 16
		if err := stage.Create(op); err != nil {
			t.Fatal(err)
		}
	}
	view := e.stageRead(stage)
	x, y := ids.OID(90002), ids.OID(90004)
	graph := dump.Graph{
		{OID: a}: nil,
		{OID: x}: nil,
		{OID: b}: {{OID: x}},
		{OID: y}: nil,
	}
	routine.OperatorDumpEvents(view, operators, graph)
	declarations := dump.DeclarationEvents([]ids.OID{a, x, b, y})
	replay := &partitionConstraintReplay{original: view}
	if err := partitionDumpEvents(context.Background(), dumpResolver{}, view, graph, declarations, nil, nil, replay, false); err != nil {
		t.Fatal(err)
	}
	routine.NormalizeOperatorDumpPredecessors(view, graph)
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	positions := make(map[dump.Event]int)
	for i, event := range order {
		positions[event] = i
	}
	for _, edge := range [][2]dump.Event{
		{{OID: x}, {OID: b}},
		{{OID: x}, {OID: y}},
		{{OID: b}, {OID: a, Phase: dump.OperatorLinks}},
		{{OID: a}, {OID: b, Phase: dump.OperatorLinks}},
	} {
		if positions[edge[0]] >= positions[edge[1]] {
			t.Fatalf("lost real prerequisite/nonoperator ordering %v: %v", edge, order)
		}
	}
	if len(graph[dump.Event{OID: x}]) != 0 {
		t.Fatal("unrelated type acquired operator completion prerequisite")
	}
}
