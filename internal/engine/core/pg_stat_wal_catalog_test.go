package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestPGStatWALCatalogIdentities(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	relation := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatWALOID})
	row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12307})
	array := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12306})
	if relation.Name() != "pg_stat_wal" || relation.RowType != 12307 || row.Array != 12306 || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("WAL view row/array ownership", relation, row, array)
	}
	if rule := relation.ReturnRuleOID(); rule == 0 || rule != pgcatalog.StatWALRuleOID || rule >= e.Catalog.NextOID() {
		t.Fatal("WAL view return-rule reservation", rule, e.Catalog.NextOID())
	}
	if relation.View != nil || !pgcatalog.RegisteredSource(read, relation) || !catalog.DefinitionOIDUsed(relation, pgcatalog.StatWALRuleOID) {
		t.Fatal("return rule must be owned builtin metadata, not an executable stored view", relation)
	}
	// Validate against every installed descriptor and owned definition, not
	// merely the neighboring relation/type/rule constants.
	if err := catalog.ValidateDefinitionOIDs(read); err != nil {
		t.Fatal("WAL view installed with colliding definition identities", err)
	}
}
