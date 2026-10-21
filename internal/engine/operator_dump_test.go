package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func TestUserOperatorDumpFinalGraph(t *testing.T) {
	for _, oids := range [][4]ids.OID{{90001, 90002, 90003, 90004}, {90003, 90004, 90001, 90002}} {
		e := New()
		stage := e.BeginCatalogStage(e.CatalogRead())
		// D.neg=C is asymmetric; A.comm=B is asymmetric; B/C are reciprocal
		// in both kinds. Allocation order must not become link-establishment order.
		d, c, a, b := oids[0], oids[1], oids[2], oids[3]
		operators := []*catalog.Operator{
			{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: d}, ObjectName: "#!#", Function: 144, Result: 16, Negator: c},
			{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: c}, ObjectName: "#?#", Function: 65, Result: 16, Commutator: b, Negator: b},
			{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: a}, ObjectName: "#~#", Function: 65, Result: 16, Commutator: b},
			{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: b}, ObjectName: "#@#", Function: 65, Result: 16, Commutator: c, Negator: c},
		}
		before := make([]catalog.Operator, len(operators))
		graph := make(dump.Graph)
		for i, op := range operators {
			op.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			op.ObjectRevision = 1
			op.Left = 23
			op.Right = 23
			op.Owner = e.roles.domain.Principal(10)
			if err := stage.Create(op); err != nil {
				t.Fatal(err)
			}
			before[i] = *op
			graph[dump.Event{OID: op.Address().ObjectID}] = nil
		}
		view := e.stageRead(stage)
		consumer := dump.Event{OID: 91000}
		graph[consumer] = []dump.Event{{OID: c}, {OID: b}, {OID: c}}
		operatorDumpEvents(view, operators, graph)
		normalizeOperatorDumpPredecessors(view, graph)
		order, err := dump.Order(context.Background(), graph, nil)
		if err != nil {
			t.Fatal(err)
		}
		positions := make(map[dump.Event]int)
		for i, event := range order {
			positions[event] = i
		}
		precedes := func(first, last dump.Event) {
			t.Helper()
			if positions[first] >= positions[last] {
				t.Fatalf("%v must precede %v: %v", first, last, order)
			}
		}
		precedes(dump.Event{OID: d}, dump.Event{OID: c})
		precedes(dump.Event{OID: a}, dump.Event{OID: b})
		for _, source := range []ids.OID{b, c} {
			completion := dump.Event{OID: source, Phase: dump.OperatorLinks}
			precedes(dump.Event{OID: b}, completion)
			precedes(dump.Event{OID: c}, completion)
			precedes(completion, consumer)
		}
		saved := make(dump.Graph)
		for key, edges := range graph {
			saved[key] = append([]dump.Event(nil), edges...)
		}
		normalizeOperatorDumpPredecessors(view, graph)
		if !reflect.DeepEqual(saved, graph) {
			t.Fatal("normalization is not idempotent")
		}
		// Retry/expansion can restore raw dependencies and introduce new phases.
		graph[consumer] = []dump.Event{{OID: b}, {OID: b}, {OID: 92000, Phase: dump.Rows}}
		graph[dump.Event{OID: 92000, Phase: dump.Rows}] = nil
		normalizeOperatorDumpPredecessors(view, graph)
		want := []dump.Event{{OID: b, Phase: dump.OperatorLinks}, {OID: 92000, Phase: dump.Rows}}
		if !reflect.DeepEqual(graph[consumer], want) {
			t.Fatal("expanded semantic prerequisites", graph[consumer])
		}
		for i, op := range operators {
			if *op != before[i] {
				t.Fatal("dump mutated captured descriptor")
			}
		}
	}
}

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
	operatorDumpEvents(view, operators, graph)
	declarations := dump.DeclarationEvents([]ids.OID{a, x, b, y})
	replay := &partitionConstraintReplay{original: view}
	if err := partitionDumpEvents(context.Background(), view, graph, declarations, nil, nil, replay, false); err != nil {
		t.Fatal(err)
	}
	normalizeOperatorDumpPredecessors(view, graph)
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
