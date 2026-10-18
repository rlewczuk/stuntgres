package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestNameDescriptors(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, ok := view.LookupType(19)
	if !ok || d.Name() != "name" {
		t.Fatal(d, ok)
	}
	run(t, e, `CREATE TABLE names(n NAME NOT NULL,t TEXT,m "name")`)
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, statement(t, "SELECT * FROM names"))
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
	bound, err := e.Describe(op, statement(t, "INSERT INTO names VALUES($1,$2,$1)"))
	if err != nil || bound.Parameters.Count != 2 {
		t.Fatal(bound, err)
	}
	if bound.Parameters.Types[0] != d.Usage() || bound.Parameters.Types[1].Type.ID() != 25 {
		t.Fatal(bound.Parameters)
	}
	_, err = e.Describe(op, statement(t, "INSERT INTO names VALUES($1,$1,$1)"))
	state(t, err, "0A000")
	for _, q := range []string{"SELECT CURRENT_USER", "SELECT SESSION_USER", "SELECT CURRENT_SCHEMA"} {
		x, err := e.Describe(op, statement(t, q))
		if err != nil || x.Results[0].Usage != d.Usage() {
			t.Fatal(q, x, err)
		}
	}
	run(t, e, "INSERT INTO names VALUES('MiXeD',NULL,NULL)")
	old := run(t, e, "SELECT * FROM names")
	ref := e.catalog.domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 19})
	other := New()
	if err = other.ValidateObjectRef(other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign NAME reference")
	}
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.ValidateObjectRef(view, ref); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(19)
	if current == d {
		t.Fatal("NAME generation reused")
	}
	rows := independentFinalRows(old.Rows)
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
	e := New()
	stale := testUsage(e, sqltype.Name)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []sqltype.Usage{testUsage(New(), sqltype.Name), stale} {
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		d := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: "invalid", revision: 1, columns: []ColumnDescriptor{{Name: "n", Usage: usage}}, index: map[string]int{"n": 0}, attributes: []int32{1}}
		d.attributeHighWater = 1
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("foreign/stale NAME published")
		}
		if e.CatalogRead().state != base.state {
			t.Fatal("catalog changed")
		}
	}
}
