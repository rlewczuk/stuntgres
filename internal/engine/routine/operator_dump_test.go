package routine

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
		descriptors := make([]catalog.Descriptor, len(operators))
		graph := make(dump.Graph)
		for i, op := range operators {
			op.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			op.ObjectRevision = 1
			op.Left = 23
			op.Right = 23
			before[i], descriptors[i] = *op, op
			graph[dump.Event{OID: op.Address().ObjectID}] = nil
		}
		view := dumpUnitView(descriptors...)
		consumer := dump.Event{OID: 91000}
		graph[consumer] = []dump.Event{{OID: c}, {OID: b}, {OID: c}}
		OperatorDumpEvents(view, operators, graph)
		NormalizeOperatorDumpPredecessors(view, graph)
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
		NormalizeOperatorDumpPredecessors(view, graph)
		if !reflect.DeepEqual(saved, graph) {
			t.Fatal("normalization is not idempotent")
		}
		// Retry/expansion can restore raw dependencies and introduce new phases.
		graph[consumer] = []dump.Event{{OID: b}, {OID: b}, {OID: 92000, Phase: dump.Rows}}
		graph[dump.Event{OID: 92000, Phase: dump.Rows}] = nil
		NormalizeOperatorDumpPredecessors(view, graph)
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
