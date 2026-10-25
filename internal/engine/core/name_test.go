package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestNameDescriptors(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d, ok := view.LookupType(19)
	if !ok || d.Name() != "name" {
		t.Fatal(d, ok)
	}
	enginetest.Run(t, e, `CREATE TABLE names(n NAME NOT NULL,t TEXT,m "name")`)
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, enginetest.Statement(t, "SELECT * FROM names"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{19, 25, 19} {
		c := desc.Results[i]
		collation := uint32(950)
		if id == 25 {
			collation = 100
		}
		if c.Usage.Type.ID() != id || !c.Usage.Valid() || c.Usage.Typmod != -1 || c.Usage.Collation != (sqltype.CollationRef{ID: collation, Derivation: sqltype.Default}) || !c.NullabilityKnown || c.Nullable != (i != 0) || c.OriginTable == 0 || c.OriginTable != desc.Results[0].OriginTable || c.OriginAttribute != int16(i+1) {
			t.Fatal(c)
		}
	}
	bound, err := e.Describe(op, enginetest.Statement(t, "INSERT INTO names VALUES($1,$2,$1)"))
	if err != nil || bound.Parameters.Count != 2 {
		t.Fatal(bound, err)
	}
	if bound.Parameters.Types[0] != d.Usage() || bound.Parameters.Types[1].Type.ID() != 25 {
		t.Fatal(bound.Parameters)
	}
	_, err = e.Describe(op, enginetest.Statement(t, "INSERT INTO names VALUES($1,$1,$1)"))
	enginetest.State(t, err, "0A000")
	for _, q := range []string{"SELECT CURRENT_USER", "SELECT SESSION_USER", "SELECT CURRENT_SCHEMA"} {
		x, err := e.Describe(op, enginetest.Statement(t, q))
		if err != nil || x.Results[0].Usage != d.Usage() {
			t.Fatal(q, x, err)
		}
	}
	enginetest.Run(t, e, "INSERT INTO names VALUES('MiXeD',NULL,NULL)")
	old := enginetest.Run(t, e, "SELECT * FROM names")
	ref := e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 19})
	other := engine.New()
	if err = enginetest.ValidateObjectRef(other, other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign NAME reference")
	}
	if err = engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err = enginetest.ValidateObjectRef(e, view, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(19)
	if current == d {
		t.Fatal("NAME generation reused")
	}
	rows := enginetest.IndependentFinalRows(old.Rows)
	defer rows.Close()
	values := make([]sqltype.Value, 3)
	if present, err := rows.Next(context.Background(), values); !present || err != nil {
		t.Fatal(present, err)
	}
	if values[0].Type() != d || values[2].Type() != d || !values[2].IsNull() {
		t.Fatal(values)
	}
	text, _ := values[0].Text()
	if text != "MiXeD" {
		t.Fatal(text)
	}
}

func TestNameCatalogRejectsForeignAndStale(t *testing.T) {
	e := engine.New()
	stale := enginetest.Usage(e, sqltype.Name)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []sqltype.Usage{enginetest.Usage(engine.New(), sqltype.Name), stale} {
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "invalid", ObjectRevision: 1, Columns: []catalog.Column{{Name: "n", Usage: usage}}, Index: map[string]int{"n": 0}, Attributes: []int32{1}}
		d.ColumnMeta.HighWater = 1
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("foreign/stale NAME published")
		}
		if e.CatalogRead().State() != base.State() {
			t.Fatal("catalog changed")
		}
	}
}
