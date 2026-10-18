package engine

import (
	"context"
	"reflect"
	"testing"
)

func TestUserOperatorDumpFinalGraph(t *testing.T) {
	for _, oids := range [][4]OID{{90001, 90002, 90003, 90004}, {90003, 90004, 90001, 90002}} {
		e := New()
		stage := e.BeginCatalogStage(e.CatalogRead())
		// D.neg=C is asymmetric; A.comm=B is asymmetric; B/C are reciprocal
		// in both kinds. Allocation order must not become link-establishment order.
		d, c, a, b := oids[0], oids[1], oids[2], oids[3]
		operators := []*operatorDescriptor{
			{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: d}, name: "#!#", function: 144, result: 16, negator: c},
			{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: c}, name: "#?#", function: 65, result: 16, commutator: b, negator: b},
			{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: a}, name: "#~#", function: 65, result: 16, commutator: b},
			{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: b}, name: "#@#", function: 65, result: 16, commutator: c, negator: c},
		}
		before := make([]operatorDescriptor, len(operators))
		graph := make(map[dumpEventKey][]dumpEventKey)
		for i, op := range operators {
			op.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
			op.revision = 1
			op.left = 23
			op.right = 23
			op.owner = e.authorizationDomain.Principal(10)
			if err := stage.Create(op); err != nil {
				t.Fatal(err)
			}
			before[i] = *op
			graph[dumpEventKey{oid: op.address.ObjectID}] = nil
		}
		view := e.stageRead(stage)
		consumer := dumpEventKey{oid: 91000}
		graph[consumer] = []dumpEventKey{{oid: c}, {oid: b}, {oid: c}}
		operatorDumpEvents(view, operators, graph)
		normalizeOperatorDumpPredecessors(view, graph)
		order, err := viewDumpOrder(context.Background(), graph, nil)
		if err != nil {
			t.Fatal(err)
		}
		positions := make(map[dumpEventKey]int)
		for i, event := range order {
			positions[event] = i
		}
		precedes := func(first, last dumpEventKey) {
			t.Helper()
			if positions[first] >= positions[last] {
				t.Fatalf("%v must precede %v: %v", first, last, order)
			}
		}
		precedes(dumpEventKey{oid: d}, dumpEventKey{oid: c})
		precedes(dumpEventKey{oid: a}, dumpEventKey{oid: b})
		for _, source := range []OID{b, c} {
			completion := dumpEventKey{oid: source, phase: dumpOperatorLinks}
			precedes(dumpEventKey{oid: b}, completion)
			precedes(dumpEventKey{oid: c}, completion)
			precedes(completion, consumer)
		}
		saved := make(map[dumpEventKey][]dumpEventKey)
		for key, edges := range graph {
			saved[key] = append([]dumpEventKey(nil), edges...)
		}
		normalizeOperatorDumpPredecessors(view, graph)
		if !reflect.DeepEqual(saved, graph) {
			t.Fatal("normalization is not idempotent")
		}
		// Retry/expansion can restore raw dependencies and introduce new phases.
		graph[consumer] = []dumpEventKey{{oid: b}, {oid: b}, {oid: 92000, phase: dumpRows}}
		graph[dumpEventKey{oid: 92000, phase: dumpRows}] = nil
		normalizeOperatorDumpPredecessors(view, graph)
		want := []dumpEventKey{{oid: b, phase: dumpOperatorLinks}, {oid: 92000, phase: dumpRows}}
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
	a, b := OID(90001), OID(90003)
	operators := []*operatorDescriptor{
		{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: a}, name: "#~#", commutator: b},
		{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: b}, name: "#@#", commutator: a},
	}
	for _, op := range operators {
		op.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
		op.revision = 1
		op.left = 23
		op.right = 23
		op.owner = e.authorizationDomain.Principal(10)
		op.function = 65
		op.result = 16
		if err := stage.Create(op); err != nil {
			t.Fatal(err)
		}
	}
	view := e.stageRead(stage)
	x, y := OID(90002), OID(90004)
	graph := map[dumpEventKey][]dumpEventKey{
		{oid: a}: nil,
		{oid: x}: nil,
		{oid: b}: {{oid: x}},
		{oid: y}: nil,
	}
	operatorDumpEvents(view, operators, graph)
	declarations := dumpDeclarationEvents([]OID{a, x, b, y})
	replay := &partitionConstraintReplay{original: view}
	if err := partitionDumpEvents(context.Background(), view, graph, declarations, nil, nil, replay, false); err != nil {
		t.Fatal(err)
	}
	normalizeOperatorDumpPredecessors(view, graph)
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	positions := make(map[dumpEventKey]int)
	for i, event := range order {
		positions[event] = i
	}
	for _, edge := range [][2]dumpEventKey{
		{{oid: x}, {oid: b}},
		{{oid: x}, {oid: y}},
		{{oid: b}, {oid: a, phase: dumpOperatorLinks}},
		{{oid: a}, {oid: b, phase: dumpOperatorLinks}},
	} {
		if positions[edge[0]] >= positions[edge[1]] {
			t.Fatalf("lost real prerequisite/nonoperator ordering %v: %v", edge, order)
		}
	}
	if len(graph[dumpEventKey{oid: x}]) != 0 {
		t.Fatal("unrelated type acquired operator completion prerequisite")
	}
}
