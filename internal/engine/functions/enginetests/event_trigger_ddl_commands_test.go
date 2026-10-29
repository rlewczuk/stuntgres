package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestEventTriggerDDLCommandsShapeValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_ddl_commands()")
	n := node.(functions.EventTriggerDDLCommands)
	procedure, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || procedure == nil || procedure.Builtin == nil {
		t.Fatal(procedure, err)
	}
	if procedure.Address() != (ids.ObjectAddress{ClassID: 1255, ObjectID: 4568}) || procedure.Result != 2249 || !procedure.ReturnsSet() || procedure.RowEstimate() != 100 || procedure.Volatility != expr.Stable {
		t.Fatal("released selected declaration", procedure)
	}
	names := []string{"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "command"}
	types := []ids.OID{26, 26, 23, 25, 25, 25, 25, 16, 32}
	outputs := procedure.OutputDeclaration()
	if outputs == nil || outputs.Len() != 9 || outputs.Inputs() != 0 || n.Validate() != nil {
		t.Fatal("nine OUT declaration", outputs)
	}
	members := make([]sqltype.CompositeMember, 9)
	for i := range members {
		members[i] = n.Usage.Type.CompositeMember(i)
		if outputs.Name(i) != names[i] || outputs.Type(i) != types[i] || members[i].Name != names[i] || members[i].Attribute != int32(i+1) || members[i].Usage.Type.ID() != sqltype.ID(types[i]) {
			t.Fatal("OUT order/type", i)
		}
	}
	for _, kind := range []string{"arity", "name", "type", "ordinal"} {
		t.Run(kind, func(t *testing.T) {
			changed := append([]sqltype.CompositeMember(nil), members...)
			switch kind {
			case "arity":
				changed = changed[:8]
			case "name":
				changed[8].Name = "other"
			case "type":
				changed[8].Usage = members[3].Usage
			case "ordinal":
				changed[8].Attribute = 10
			}
			shape, err := sqltype.RecordShape(changed)
			if err != nil {
				t.Fatal(err)
			}
			bad := n
			bad.Usage = shape.Usage()
			if bad.Validate() == nil {
				t.Fatal("malformed shape admitted")
			}
		})
	}
	if n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || !n.Properties().StageDependent {
		t.Fatal("collector frozen at Prepare")
	}
}

func TestEventTriggerDDLCommandsRebuildOwnership(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_ddl_commands()")
	n := node.(functions.EventTriggerDDLCommands)
	if _, err := n.OpenSource(&evaluation); err == nil {
		t.Fatal("binding granted lexical context authority")
	}
	info := n.Info
	info.Occurrence++
	rebuilt, err := n.Rebuild(info, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := rebuilt.(functions.EventTriggerDDLCommands)
	if !reflect.DeepEqual(got, functions.EventTriggerDDLCommands{Info: info}) || got.Usage.Type != n.Usage.Type || got.RoutineReference != n.RoutineReference {
		t.Fatal("retained metadata/provenance changed")
	}
	first := &core.EventCommand{Tag: "GRANT", CollectDDL: true, Commands: []sqltype.DDLCommand{{Tag: "GRANT", SpecialKind: "TABLE"}}}
	scope.EventCommand = first
	a, err := got.OpenSource(&evaluation)
	if err != nil || len(a.Values) != 1 {
		t.Fatal(a, err)
	}
	defer a.Close()
	b, err := got.OpenSource(&evaluation)
	if err != nil || len(b.Values) != 1 || len(first.Commands) != 1 {
		t.Fatal("opening consumed collector", b, err)
	}
	b.Close()
	if a.Values[0].CompositeField(8).IsNull() {
		t.Fatal("opaque command was NULL")
	}
	scope.EventCommand = &core.EventCommand{Tag: "DROP TABLE", CollectDDL: true}
	empty, err := got.OpenSource(&evaluation)
	if err != nil || len(empty.Values) != 0 {
		t.Fatal("rebuild retained old bag", empty, err)
	}
	empty.Close()
	scope.EventCommand = nil
	if len(a.Values) != 1 || a.Values[0].CompositeField(8).IsNull() {
		t.Fatal("lexical exit invalidated owned query output")
	}
}

type ddlCommandsWrappingEvaluation struct {
	*exec.Context
	active *droppedWrappingContext
}

func (c *ddlCommandsWrappingEvaluation) EventTriggerDDLCommands() ([][]sqltype.Value, error) {
	rows, err := c.Context.EventTriggerDDLCommands()
	if err == nil {
		c.active.armed = true
	}
	return rows, err
}

func TestEventTriggerDDLCommandsWrappingCancellation(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during_wrapping"}[during], func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_event_trigger_ddl_commands()")
			n := node.(functions.EventTriggerDDLCommands)
			command := &core.EventCommand{Tag: "GRANT", CollectDDL: true}
			for range 8 {
				command.Commands = append(command.Commands, sqltype.DDLCommand{Tag: "GRANT", SpecialKind: "TABLE"})
			}
			scope.EventCommand = command
			before := append([]sqltype.DDLCommand(nil), command.Commands...)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &droppedWrappingContext{Context: base, cancel: cancel}
			evaluation.Context = active
			if !during {
				cancel()
			}
			source, err := n.OpenSource(&ddlCommandsWrappingEvaluation{Context: &evaluation, active: active})
			if err != context.Canceled || source.Type != nil || source.Values != nil || source.Advance != nil || source.State != nil {
				t.Fatal("cancellation returned source prefix", source, err)
			}
			if during && active.polls < 3 {
				t.Fatal("did not cancel during composite wrapping")
			}
			if scope.EventCommand != command || !reflect.DeepEqual(command.Commands, before) {
				t.Fatal("cancellation consumed command state")
			}
			evaluation.Context = context.Background()
			again, err := n.OpenSource(&evaluation)
			if err != nil || len(again.Values) != 8 {
				t.Fatal("collector unhealthy after cancellation", again, err)
			}
			again.Close()
		})
	}
}
