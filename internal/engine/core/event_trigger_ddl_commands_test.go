package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func TestEventTriggerDDLCommandAppendOwnership(t *testing.T) {
	var scope core.StatementScope
	a := ids.ObjectAddress{ClassID: 1259, ObjectID: 50000}
	scope.RecordDDLCommand("CREATE SEQUENCE", a)
	if scope.EventCommand != nil {
		t.Fatal("inactive collector allocated lexical state")
	}
	scope.EventCommand = &core.EventCommand{Tag: "CREATE TABLE"}
	scope.RecordDDLCommand("CREATE SEQUENCE", a)
	if scope.EventCommand.Commands != nil {
		t.Fatal("start-only command allocated collection")
	}
	scope.EventCommand.CollectDDL = true
	scope.RecordDDLCommand("CREATE SEQUENCE", a)
	scope.RecordDDLCommand("ALTER SEQUENCE", a)
	if len(scope.EventCommand.Commands) != 2 || scope.EventCommand.Commands[0].Tag != "CREATE SEQUENCE" || scope.EventCommand.Commands[1].Tag != "ALTER SEQUENCE" || scope.EventCommand.Commands[0].ObjectID != scope.EventCommand.Commands[1].ObjectID {
		t.Fatal("append multiplicity/base tag lost", scope.EventCommand.Commands)
	}
}

func TestEventTriggerDDLCommandCheckpoint(t *testing.T) {
	_, scope := plExceptionUnitScope(t)
	outer := &core.EventCommand{Tag: "CREATE TABLE", Phase: "ddl_command_end", CollectDDL: true}
	scope.EventCommand = outer
	a := ids.ObjectAddress{ClassID: 1259, ObjectID: 50000}
	scope.RecordDDLCommand("CREATE SEQUENCE", a)
	before := outer.Commands[0]
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	scope.RecordDDLCommand("ALTER SEQUENCE", a)
	retained := outer.Commands
	scope.EventCommand = &core.EventCommand{Tag: "ALTER TABLE"}
	if err := scope.EndPLException(mark, true, errors.New("caught")); err != nil {
		t.Fatal(err)
	}
	if scope.EventCommand != outer || !outer.CollectDDL || outer.Phase != "ddl_command_end" || len(outer.Commands) != 1 || outer.Commands[0] != before {
		t.Fatal("failed suffix/checkpoint pointer restoration", outer)
	}
	scope.RecordDDLCommand("ALTER SEQUENCE", a)
	if len(retained) != 2 || retained[0] != before || retained[1].Tag != "ALTER SEQUENCE" || &retained[0] == &outer.Commands[0] {
		t.Fatal("checkpoint did not detach mutable append backing")
	}
}
