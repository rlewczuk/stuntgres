package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []catalog.Column
	if !empty {
		columns = []catalog.Column{{Name: "id", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: testUsage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.Mu.Lock()
	source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "pair", auth.State.CurrentUser, append([]catalog.Column(nil), columns...), index)
	if err == nil {
		source.RelationKind = catalog.CompositeRelation
		if !empty {
			source.Attributes = []int32{2, 4}
			source.ColumnMeta.HighWater = 4
			source.Columns[0].OriginAttribute = 2
			source.Columns[1].OriginAttribute = 4
		}
		err = e.StageRowTypeLocked(stage, source)
	}
	var tbl *catalog.Relation
	if err == nil {
		tbl, err = table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "typed", source.Owner, append([]catalog.Column(nil), columns...), index)
	}
	if err == nil {
		tbl.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
		if !empty {
			tbl.Attributes = []int32{1, 7}
			tbl.ColumnMeta.HighWater = 7
			tbl.Columns[1].OriginAttribute = 7
		}
		err = e.StageRowTypeLocked(stage, tbl)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(tbl); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.MustRelation(source.Address()), view.MustRelation(tbl.Address())
}
