package proc

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func routineTestType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d
		}
	}
	t.Fatal("missing routine test type", id)
	return nil
}

func TestSQLRoutineFrameIsolation(t *testing.T) {
	typ := routineTestType(t, sqltype.Int4)
	definition, err := New("SELECT $1+1", parser.Position{}, []sqltype.Usage{typ.Usage()}, typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	first, second := NewFrame(definition.Inputs), NewFrame(definition.Inputs)
	defer second.Close()
	for _, tc := range []struct {
		frame *Frame
		input string
	}{{first, "4"}, {second, "9"}} {
		value, err := typ.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		if err = tc.frame.Bind(0, value, typ.Usage()); err != nil {
			t.Fatal(err)
		}
	}
	if first.Parameters[0] == second.Parameters[0] {
		t.Fatal("invocation values shared")
	}
	first.Close()
	if second.Parameters[0].Type() != typ || second.Parameters[0].IsNull() {
		t.Fatal("closing sibling invalidated live slots")
	}
	if err := first.Bind(0, second.Parameters[0], typ.Usage()); err == nil {
		t.Fatal("closed frame admitted argument")
	}
	first.Close()
	if definition.Query == nil || len(definition.Inputs) != 1 || definition.Source != "SELECT $1+1" {
		t.Fatal("frame changed immutable declaration")
	}
}

func TestSQLRoutineFrameResultOwnership(t *testing.T) {
	typ := routineTestType(t, sqltype.Bytea)
	value, err := typ.Input(context.Background(), `\x0001ff`, -1)
	if err != nil {
		t.Fatal(err)
	}
	frame := NewFrame(nil)
	result, err := frame.Result(value, typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	frame.Close()
	text, err := typ.Output(context.Background(), result)
	if err != nil || text != `\x0001ff` {
		t.Fatal("result did not survive frame release", text, err)
	}
	if _, err := frame.Result(value, typ.Usage()); err == nil {
		t.Fatal("closed frame returned a result")
	}
}
