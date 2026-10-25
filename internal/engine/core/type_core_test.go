package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTypeCoreCatalogDescriptionsAndOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, id := range []sqltype.ID{16, 20, 23, 25, 705} {
		d, ok := view.LookupType(id)
		if !ok || d.ID() != id {
			t.Fatal(id, d, ok)
		}
	}
	enginetest.Run(t, e, "CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)")
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, enginetest.Statement(t, "SELECT * FROM typed"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{23, 20, 25, 16} {
		c := desc.Results[i]
		if c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Nullable != (i != 0) || !c.NullabilityKnown || c.OriginTable == 0 || c.OriginTable != desc.Results[0].OriginTable || c.OriginAttribute != int16(i+1) {
			t.Fatal(c)
		}
	}
	enginetest.Run(t, e, "CREATE TABLE repeats(a INT,b TEXT,c INT)")
	p, err := e.Describe(op, enginetest.Statement(t, "INSERT INTO repeats VALUES($1,$2,$1)"))
	if err != nil || p.Parameters.Count != 2 || p.Parameters.Types[0].Type.ID() != 23 || p.Parameters.Types[1].Type.ID() != 25 {
		t.Fatal(p, err)
	}
	for _, q := range []string{"INSERT INTO repeats VALUES($2147483647,'x',1)", "INSERT INTO repeats VALUES($2,'x',$2)"} {
		_, err = e.Describe(op, enginetest.Statement(t, q))
		enginetest.State(t, err, "42P18")
	}
	for _, q := range []string{"INSERT INTO typed VALUES($1,$1,'x',TRUE)", "INSERT INTO repeats VALUES($1,$1,1)"} {
		_, err = e.Describe(op, enginetest.Statement(t, q))
		enginetest.State(t, err, "0A000")
	}
	enginetest.Run(t, e, "INSERT INTO typed VALUES(1,NULL,NULL,NULL)")
	old := enginetest.Run(t, e, "SELECT * FROM typed")
	values := make([]sqltype.Value, 4)
	first := enginetest.IndependentFinalRows(old.Rows)
	if present, nextErr := first.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	first.Close()
	for i, v := range values {
		if v.Type() != desc.Results[i].Usage.Type || v.IsNull() != (i != 0) {
			t.Fatal(v)
		}
	}
	ref := e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	other := engine.New()
	if err = enginetest.ValidateObjectRef(other, other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign type ref")
	}
	if err = engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err = enginetest.ValidateObjectRef(e, view, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(23)
	if current == desc.Results[0].Usage.Type {
		t.Fatal("reused type generation")
	}
	later := enginetest.IndependentFinalRows(old.Rows)
	if present, nextErr := later.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	later.Close()
	if values[0].Type() != desc.Results[0].Usage.Type {
		t.Fatal("retained type changed")
	}
}

func TestTypeCoreCatalogRejectsNoncurrentColumnTypes(t *testing.T) {
	e := engine.New()
	stale := enginetest.Usage(e, sqltype.Int4)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []sqltype.Usage{{}, enginetest.Usage(e, sqltype.Unknown), enginetest.Usage(engine.New(), sqltype.Int4), stale} {
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		descriptor := &catalog.Relation{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
			ObjectName:      "invalid", ObjectRevision: 1,
			Columns: []catalog.Column{{Name: "id", Usage: usage}},
			Index:   map[string]int{"id": 0}, Attributes: []int32{1},
			ColumnMeta: catalog.ColumnMetadata{
				HighWater: 1,
			},
		}
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("invalid column type accepted", usage)
		}
		if e.CatalogRead().State() != base.State() {
			t.Fatal("invalid type published")
		}
	}
}

func TestFloatCatalogDescriptionsAndOwnership(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE f(a REAL NOT NULL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53),j "float4",k "float8")`)
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, enginetest.Statement(t, "SELECT * FROM f"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{700, 700, 701, 701, 701, 700, 700, 701, 701, 700, 701} {
		c := desc.Results[i]
		if c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Usage.Collation != (sqltype.CollationRef{}) || c.Nullable != (i != 0) || !c.NullabilityKnown || c.OriginTable == 0 || c.OriginTable != desc.Results[0].OriginTable || c.OriginAttribute != int16(i+1) {
			t.Fatal(c)
		}
	}
	bound, err := e.Describe(op, enginetest.Statement(t, "INSERT INTO f(a,c) VALUES($1,$2)"))
	if err != nil || bound.Parameters.Types[0].Type.ID() != 700 || bound.Parameters.Types[1].Type.ID() != 701 {
		t.Fatal(bound, err)
	}
}

func TestFloatFailedRestorePreservesEpochAndCatalog(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE f(r REAL,d DOUBLE PRECISION)")
	enginetest.Run(t, e, "INSERT INTO f VALUES('7.038531e-26','-0')")
	before := e.CatalogRead()
	var original bytes.Buffer
	if err := engine.Dump(context.Background(), e, &original); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')", "CREATE TABLE replacement(d DOUBLE PRECISION); INSERT INTO replacement VALUES('1'),('1e999')"} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore")
		}
		if current := e.CatalogRead(); current.State() != before.State() || current.Epoch() != before.Epoch() {
			t.Fatal("failed float restore changed catalog generation")
		}
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), e, &dump); err != nil || dump.String() != original.String() {
			t.Fatal(dump.String(), err)
		}
	}
}
