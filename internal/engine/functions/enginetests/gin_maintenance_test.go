package enginetests_test

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGINMaintenanceTypedRecipe(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "gin_clean_pending_list(NULL::regclass)")
	n, ok := node.(functions.GinMaintenanceCall)
	if !ok {
		t.Fatal("not a retained typed maintenance recipe", node)
	}
	declaration, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || declaration == nil || declaration.Address().ObjectID != 3789 || !declaration.Strict || declaration.Volatility != expr.Volatile || declaration.Builtin.Parallel != 'u' {
		t.Fatal("routine provider identity", declaration, err)
	}
	if n.Usage.Type.ID() != sqltype.Int8 || n.Arg.ResultUsage().Type.ID() != sqltype.Regclass || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
		t.Fatal("typed volatile nonfolding carrier")
	}
	value, err := n.Eval(&evaluation)
	if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
		t.Fatal("typed NULL without target authority", value, err)
	}
	operands, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Count != 1 {
		t.Fatal("capture shape", operands, err)
	}
	child, err := n.CaptureOperand(0)
	if err != nil || !reflect.DeepEqual(child, n.Arg) {
		t.Fatal("captured operand", err)
	}
	if _, err := n.CaptureOperand(1); err == nil {
		t.Fatal("nonexistent captured operand")
	}
	children := n.Children()
	rebuilt, err := n.Rebuild(n.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	children[0] = nil
	next := rebuilt.(functions.GinMaintenanceCall)
	if next.Arg == nil || next.RoutineReference != n.RoutineReference {
		t.Fatal("rebuild borrowed scratch or lost provenance")
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil {
		t.Fatal("missing child accepted")
	}
	rendered, err := n.Render()
	if err != nil || rendered.Form != expr.FormCall || rendered.Name != "gin_clean_pending_list" || len(rendered.Inputs) != 1 {
		t.Fatal("rendering", rendered, err)
	}
	replacement := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, descriptor := range sqltype.Builtins() {
		replacement[descriptor.ID()] = descriptor
	}
	mapped, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(descriptor *sqltype.Descriptor) *sqltype.Descriptor { return replacement[descriptor.ID()] })
	if err != nil || !changed {
		t.Fatal("retag", changed, err)
	}
	retagged := mapped.(functions.GinMaintenanceCall)
	if retagged.Usage.Type != replacement[sqltype.Int8] || retagged.Arg.ResultUsage().Type != replacement[sqltype.Regclass] || retagged.RoutineReference != n.RoutineReference {
		t.Fatal("generation-owned typed carrier lost")
	}
	value, err = retagged.Eval(&evaluation)
	if err != nil || !value.IsNull() || value.Type() != replacement[sqltype.Int8] || n.Usage.Type == replacement[sqltype.Int8] {
		t.Fatal("retag mutated old recipe or lost nullable result", err)
	}
	t.Run("physical-regclass-identity", func(t *testing.T) {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, "gin_clean_pending_list(0::regclass)")
		call := node.(functions.GinMaintenanceCall)
		argument, err := call.Arg.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		if oid, err := argument.ClassOID(); err != nil || oid != 0 || argument.IsNull() {
			t.Fatal("not a physical regclass operand", argument, err)
		}
		_, err = call.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "could not open relation with OID 0" {
			t.Fatal("typed regclass did not reach ordinary target admission", err)
		}
	})
}
