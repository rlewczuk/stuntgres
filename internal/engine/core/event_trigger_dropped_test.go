package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestEventTriggerDroppedRecords(t *testing.T) {
	var scope core.StatementScope
	scope.EventCommand = &core.EventCommand{Tag: "DROP TABLE"}
	a := ids.ObjectAddress{ClassID: 1259, ObjectID: 50000}
	b := a
	b.SubID = 2
	scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: a, Found: true, Original: true, Identity: "public.first", Names: []string{"public", "first"}, Args: []string{}}, {Address: b, Found: true, Identity: "public.first.second"}})
	scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: a, Found: true, Normal: true, Identity: "public.reused"}, {Address: ids.ObjectAddress{ClassID: ids.EventTriggerClassID, ObjectID: 50001}, Found: true}})
	if !scope.EventCommand.Dropped || len(scope.EventCommand.Records) != 2 {
		t.Fatal("full address collection or exclusion", scope.EventCommand)
	}
	record := scope.EventCommand.Records[0]
	if !record.Original || !record.Normal || record.Identity != "public.first" || record.Names[1] != "first" || record.Args == nil {
		t.Fatal("provenance replaced captured generation", record)
	}
}

func TestDropProvenanceUnion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION dropped_owner() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER dropped_owner ON sql_drop EXECUTE FUNCTION dropped_owner()")
	enginetest.Run(t, e, "CREATE TABLE dropped_owner_table(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)")
	base := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "dropped_owner_table")
	for _, reverse := range []bool{false, true} {
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		flags := []catalog.DropProvenance{{Original: true}, {Normal: true}, {}}
		if reverse {
			flags[0], flags[1] = flags[1], flags[0]
		}
		for _, flag := range flags {
			stage.MarkDrop(ref.Address, flag)
		}
		if got := stage.DropFlags[ref.Address]; !got.Original || !got.Normal {
			t.Fatal("independent root/normal union", got)
		}
		if err := stage.Remove(ref); err != nil {
			t.Fatal(err)
		}
		if err := core.StageDropProvider(context.Background(), stage, nil, nil, nil).ExpressionDependencies([]ids.ObjectAddress{ref.Address}, parser.DropCascade, ids.ObjectAddress{}); err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, removal := range stage.Removals() {
			for _, record := range removal.Dropped {
				counts[record.Kind]++
				if record.Address != ref.Address && (record.Original || record.Normal) {
					t.Fatal("ancestor provenance spread", record)
				}
			}
		}
		if counts["table"] != 1 || counts["type"] != 2 || counts["default value"] != 1 || counts["table constraint"] != 2 || counts["index"] != 1 {
			t.Fatal("incomplete logical payload capture", counts)
		}
		internal := e.BeginCatalogStage(base)
		p := core.StageDropProvider(context.Background(), internal, nil, nil, nil)
		if err := p.RemoveInternal(ref); err != nil {
			t.Fatal(err)
		}
		for _, removal := range internal.Removals() {
			if !removal.Internal || len(removal.Dropped) != 0 {
				t.Fatal("operation-internal capture escaped", removal)
			}
		}
		e.DiscardCatalogStage(internal)
	}
}

func TestEventTriggerDroppedCheckpoint(t *testing.T) {
	_, scope := plExceptionUnitScope(t)
	outer := &core.EventCommand{Tag: "DROP TABLE", Phase: "sql_drop"}
	scope.EventCommand = outer
	a := ids.ObjectAddress{ClassID: 1259, ObjectID: 50000}
	scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: a, Found: true, Original: true, Identity: "public.before"}})
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: a, Found: true, Normal: true}, {Address: ids.ObjectAddress{ClassID: 1259, ObjectID: 50001}, Found: true}})
	scope.EventCommand = &core.EventCommand{Tag: "ALTER TABLE", Phase: "ddl_command_end"}
	if err := scope.EndPLException(mark, true, errors.New("caught")); err != nil {
		t.Fatal(err)
	}
	if scope.EventCommand != outer || outer.Phase != "sql_drop" || len(outer.Records) != 1 || outer.Records[0].Normal || !outer.Records[0].Original {
		t.Fatal("checkpoint lost pointer/phase or retained merged flags/suffix", outer)
	}
	scope.RecordDroppedObjects([]catalog.DroppedObject{{Address: a, Found: true, Normal: true}})
	if len(outer.Records) != 1 || !outer.Records[0].Normal {
		t.Fatal("checkpoint failed to restore address index", outer)
	}
}
