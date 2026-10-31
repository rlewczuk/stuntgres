package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func TestDropClosureNormalAutomaticUnion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	enginetest.Run(t, e, "CREATE TABLE closure_serial(s serial)")
	base := e.CatalogRead()
	relation := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_serial").Address)
	column := relation.Address()
	column.SubID = relation.Attributes[0]
	sequence := enginetest.RoleTableRef(t, e, "closure_serial_s_seq").Address
	attrdef := ids.ObjectAddress{ClassID: 2604, ObjectID: relation.ColumnMeta.Defaults[column.SubID].OID}
	for _, roots := range [][]ids.ObjectAddress{{column, sequence}, {sequence, column}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		edges, err := view.DropClosure(reader, roots)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, edge := range edges {
			if edge.Address == attrdef {
				found++
				if !edge.Normal || !edge.Automatic {
					t.Fatal("NORMAL and automatic permission are independent", edge)
				}
			}
		}
		if found != 1 || !stage.DropFlags[attrdef].Normal || stage.DropFlags[relation.Address()].Normal || stage.RemovalCount() != 0 {
			t.Fatal("union duplicated effects or leaked to whole owner", edges)
		}
		e.DiscardCatalogStage(stage)
	}
}
