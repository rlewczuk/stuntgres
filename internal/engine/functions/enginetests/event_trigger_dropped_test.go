package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var droppedOutputNames = []string{"classid", "objid", "objsubid", "original", "normal", "is_temporary", "object_type", "schema_name", "object_name", "object_identity", "address_names", "address_args"}
var droppedOutputTypes = []ids.OID{26, 26, 23, 16, 16, 16, 25, 25, 25, 25, 1009, 1009}

func TestEventTriggerDroppedShapeValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_dropped_objects()")
	n := node.(functions.EventTriggerDroppedObjects)
	procedure, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || procedure == nil || procedure.Builtin == nil {
		t.Fatal("missing selected builtin declaration", procedure, err)
	}
	if procedure.Address() != (ids.ObjectAddress{ClassID: 1255, ObjectID: 3566}) || procedure.Name() != "pg_event_trigger_dropped_objects" || procedure.Signature != "" || procedure.Result != 2249 || !procedure.ReturnsSet() || procedure.RowEstimate() != 100 || procedure.Volatility != expr.Stable {
		t.Fatal("released declaration identity/shape", procedure)
	}
	outputs := procedure.OutputDeclaration()
	if outputs == nil || outputs.Len() != 12 || outputs.Inputs() != 0 || n.Usage.Type.ID() != 2249 || n.Usage.Type.CompositeLen() != 12 || n.Validate() != nil {
		t.Fatal("twelve-column record declaration", outputs, n)
	}
	members := make([]sqltype.CompositeMember, 12)
	for i := range members {
		members[i] = n.Usage.Type.CompositeMember(i)
		if outputs.Name(i) != droppedOutputNames[i] || outputs.Type(i) != droppedOutputTypes[i] || members[i].Name != droppedOutputNames[i] || members[i].Usage.Type.ID() != sqltype.ID(droppedOutputTypes[i]) || members[i].Attribute != int32(i+1) {
			t.Fatal("independent OUT order/name/type", i, members[i])
		}
	}
	if len(n.Children()) != 0 || n.PlanningFold() != expr.FoldNever || n.IntrinsicVolatility() != expr.Stable || !n.Properties().StageDependent {
		t.Fatal("source must be childless, stable and nonfoldable", n)
	}
	for _, kind := range []string{"nil", "scalar", "arity", "name", "type"} {
		t.Run(kind, func(t *testing.T) {
			bad := n
			switch kind {
			case "nil":
				bad.Usage.Type = nil
			case "scalar":
				bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
			default:
				changed := append([]sqltype.CompositeMember(nil), members...)
				switch kind {
				case "arity":
					changed = changed[:11]
				case "name":
					changed[4].Name = "not_normal"
				case "type":
					changed[4].Usage = members[6].Usage
				}
				shape, err := sqltype.RecordShape(changed)
				if err != nil {
					t.Fatal(err)
				}
				bad.Usage = shape.Usage()
			}
			if bad.Validate() == nil {
				t.Fatal("malformed dropped-object shape accepted")
			}
			if _, err := n.Rebuild(bad.Info, nil); err == nil {
				t.Fatal("Rebuild bypassed shape validation")
			}
		})
	}
	if _, err := n.Rebuild(n.Info, []expr.Expr{n}); err == nil {
		t.Fatal("Rebuild accepted an invocation child")
	}
}

func TestEventTriggerDroppedRebuildOwnership(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_dropped_objects()")
	n := node.(functions.EventTriggerDroppedObjects)
	original := n
	info := n.Info
	info.Occurrence++
	rebuilt, err := n.Rebuild(info, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := rebuilt.(functions.EventTriggerDroppedObjects)
	if !reflect.DeepEqual(got, functions.EventTriggerDroppedObjects{Info: info}) || got.Usage.Type != n.Usage.Type || got.RoutineReference != n.RoutineReference || !reflect.DeepEqual(n, original) {
		t.Fatal("Rebuild lost immutable metadata/backing or retained extra invocation state")
	}
	// Open the same rebuilt carrier across two lexical commands. The old command
	// stays alive: fresh output therefore cannot be explained by consuming it.
	first := &core.EventCommand{Tag: "DROP TABLE", Phase: "sql_drop", Dropped: true, Records: []catalog.DroppedObject{{Address: ids.ObjectAddress{ClassID: 1259, ObjectID: 50000}, Found: true, Original: true, Kind: "table", Identity: "public.first", Names: []string{"public", "first"}, Args: []string{}}}}
	scope.EventCommand = first
	a, err := got.OpenSource(&evaluation)
	if err != nil || len(a.Values) != 1 {
		t.Fatal("first live opening", a, err)
	}
	defer a.Close()
	scope.EventCommand = &core.EventCommand{Tag: "DROP TABLE", Phase: "sql_drop", Dropped: true}
	b, err := got.OpenSource(&evaluation)
	if err != nil || len(b.Values) != 0 || len(first.Records) != 1 || !first.Records[0].Original {
		t.Fatal("carrier retained prior graph/source/records", b, err)
	}
	b.Close()
	// Immutable declaration and shape are not an invocation capability.
	withoutHost := evaluation
	withoutHost.Host = nil
	if source, err := got.OpenSource(&withoutHost); err == nil || source.Type != nil || source.Values != nil || source.State != nil || source.Advance != nil {
		t.Fatal("retained declaration granted invocation authority", source, err)
	}
	if !reflect.DeepEqual(got, functions.EventTriggerDroppedObjects{Info: info}) || len(a.Values) != 1 {
		t.Fatal("opening mutated carrier or invalidated previous owned source")
	}
}

// Isolate composite wrapping from the core conversion: the real runtime first
// converts the live command under a healthy operation context, then arms the
// evaluation context only when its complete typed rows have been returned.
type droppedWrappingContext struct {
	context.Context
	cancel context.CancelFunc
	armed  bool
	polls  int
}

func (c *droppedWrappingContext) Err() error {
	if c.armed {
		c.polls++
		if c.polls == 3 {
			c.cancel()
		}
	}
	return c.Context.Err()
}

type droppedWrappingEvaluation struct {
	*exec.Context
	active   *droppedWrappingContext
	returned bool
}

func (c *droppedWrappingEvaluation) EventTriggerDroppedObjects() ([][]sqltype.Value, error) {
	rows, err := c.Context.EventTriggerDroppedObjects()
	if err == nil {
		c.returned = true
		c.active.armed = true
	}
	return rows, err
}

func TestEventTriggerDroppedWrappingCancellation(t *testing.T) {
	for _, inProgress := range []bool{false, true} {
		name := "pre_canceled"
		if inProgress {
			name = "accumulated_composites"
		}
		t.Run(name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_dropped_objects()")
			n := node.(functions.EventTriggerDroppedObjects)
			command := &core.EventCommand{Tag: "DROP TABLE", Phase: "sql_drop"}
			scope.EventCommand = command
			for i := range 8 {
				scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: ids.ObjectAddress{ClassID: 1259, ObjectID: ids.OID(51000 + i)}, Found: true, Original: i == 0, Normal: i%2 == 0, Kind: "table", Identity: "public.kept", Names: []string{"public", "kept"}, Args: []string{}}})
			}
			before := make([]catalog.DroppedObject, len(command.Records))
			for i, record := range command.Records {
				before[i] = record
				before[i].Names = append([]string(nil), record.Names...)
				before[i].Args = append([]string{}, record.Args...)
			}
			carrier := n
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &droppedWrappingContext{Context: base, cancel: cancel}
			evaluation.Context = active
			probe := &droppedWrappingEvaluation{Context: &evaluation, active: active}
			if !inProgress {
				cancel()
			}
			source, err := n.OpenSource(probe)
			if err != context.Canceled || source.Type != nil || source.Values != nil || source.State != nil || source.Advance != nil {
				t.Errorf("interrupted wrapping = %v, %v; want no source and ordinary context.Canceled", source, err)
			}
			if inProgress && (!probe.returned || active.polls < 3) {
				t.Error("wrapping never reached cancellation after runtime conversion")
			}
			if !inProgress && probe.returned {
				t.Error("pre-canceled opening reached the runtime")
			}
			if scope.EventCommand != command || command.Phase != "sql_drop" || !command.Dropped || !reflect.DeepEqual(command.Records, before) || !reflect.DeepEqual(n, carrier) {
				t.Fatal("interruption changed retained carrier/records/flags/authority")
			}
			evaluation.Context = context.Background()
			again, err := n.OpenSource(&evaluation)
			if err != nil || len(again.Values) != 8 {
				t.Fatal("interruption consumed the command", again, err)
			}
			again.Close()
		})
	}
}
