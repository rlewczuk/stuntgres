package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func TestEventTriggerDDLCommandsConversionCancellation(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unit rollback"))
			scope.Inquiry.CommentCatalog = e.CatalogRead()
			command := &core.EventCommand{Tag: "GRANT", CollectDDL: true}
			scope.EventCommand = command
			for range 8 {
				scope.RecordDDLPrivileges("GRANT", "TABLE")
			}
			before := append([]sqltype.DDLCommand(nil), command.Commands...)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &droppedConversionContext{Context: base, cancel: cancel}
			if !during {
				cancel()
			}
			op.Ctx = active
			runtime := core.HostOf(scope).(functions.EventTriggerDDLCommandsRuntime)
			rows, err := runtime.EventTriggerDDLCommands()
			if err != context.Canceled || rows != nil {
				t.Fatal("interrupted runtime published rows", rows, err)
			}
			if during && active.polls < 3 {
				t.Fatal("did not cross conversion cancellation boundary")
			}
			if scope.EventCommand != command || !reflect.DeepEqual(command.Commands, before) {
				t.Fatal("conversion consumed command backing")
			}
			op.Ctx = context.Background()
			again, err := runtime.EventTriggerDDLCommands()
			if err != nil || len(again) != len(before) {
				t.Fatal("collector unhealthy after interruption", again, err)
			}
		})
	}
}
