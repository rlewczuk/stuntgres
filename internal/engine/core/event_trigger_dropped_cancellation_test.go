package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// Arm only after statement setup. The third cooperative poll is beyond entry
// and the first row boundary, so cancellation cannot be satisfied by entry-only
// checking. There is no timer, worker or SQL executor involved.
type droppedConversionContext struct {
	context.Context
	cancel context.CancelFunc
	polls  int
}

func (c *droppedConversionContext) Err() error {
	c.polls++
	if c.polls == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestEventTriggerDroppedConversionCancellation(t *testing.T) {
	for _, inProgress := range []bool{false, true} {
		name := "pre_canceled"
		if inProgress {
			name = "accumulated_rows"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Inquiry.CommentCatalog = e.CatalogRead()
			defer scope.Finish(errors.New("unit rollback"))
			command := &core.EventCommand{Tag: "DROP TABLE", Phase: "sql_drop"}
			scope.EventCommand = command
			for i := range 8 {
				scope.RecordDroppedObjects([]catalog.DroppedObject{{
					Address: ids.ObjectAddress{ClassID: 1259, ObjectID: ids.OID(50000 + i)},
					Found:   true, Original: i == 0, Normal: i%2 == 0,
					Kind: "table", HasSchema: true, Schema: "public", HasName: true, Name: "captured",
					Identity: "public.captured", Names: []string{"public", "captured"}, Args: []string{},
				}})
			}
			before := make([]catalog.DroppedObject, len(command.Records))
			for i, record := range command.Records {
				before[i] = record
				before[i].Names = append([]string(nil), record.Names...)
				before[i].Args = append([]string{}, record.Args...)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &droppedConversionContext{Context: base, cancel: cancel}
			if !inProgress {
				cancel()
			}
			op.Ctx = active
			runtime := core.HostOf(scope).(functions.EventTriggerDroppedRuntime)
			rows, err := runtime.EventTriggerDroppedObjects()
			if err != context.Canceled || rows != nil {
				t.Errorf("interrupted conversion = %v, %v; want nil rows and ordinary context.Canceled", rows, err)
			}
			if inProgress && active.polls < 3 {
				t.Error("conversion never reached the in-progress cancellation boundary")
			}
			if scope.EventCommand != command || command.Phase != "sql_drop" || !command.Dropped || !reflect.DeepEqual(command.Records, before) {
				t.Fatal("conversion mutated retained carrier, records, flags or lexical authority")
			}
			op.Ctx = context.Background()
			again, err := runtime.EventTriggerDroppedObjects()
			if err != nil || len(again) != len(before) {
				t.Fatal("interruption consumed the retained records", len(again), err)
			}
		})
	}
}
