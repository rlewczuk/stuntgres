package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTypeCoreCatalogDescriptionsAndOwnership(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, id := range []sqltype.ID{16, 20, 23, 25, 705} {
		d, ok := view.LookupType(id)
		if !ok || d.ID() != id {
			t.Fatal(id, d, ok)
		}
	}
	run(t, e, "CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, statement(t, "SELECT * FROM typed"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{23, 20, 25, 16} {
		c := desc.Results[i]
		if c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Nullable != (i != 0) || !c.NullabilityKnown || c.OriginTable == 0 || c.OriginTable != desc.Results[0].OriginTable || c.OriginAttribute != int16(i+1) {
			t.Fatal(c)
		}
	}
	run(t, e, "CREATE TABLE repeats(a INT,b TEXT,c INT)")
	p, err := e.Describe(op, statement(t, "INSERT INTO repeats VALUES($1,$2,$1)"))
	if err != nil || p.Parameters.Count != 2 || p.Parameters.Types[0].Type.ID() != 23 || p.Parameters.Types[1].Type.ID() != 25 {
		t.Fatal(p, err)
	}
	for _, q := range []string{"INSERT INTO repeats VALUES($2147483647,'x',1)", "INSERT INTO repeats VALUES($2,'x',$2)"} {
		_, err = e.Describe(op, statement(t, q))
		state(t, err, "42P18")
	}
	for _, q := range []string{"INSERT INTO typed VALUES($1,$1,'x',TRUE)", "INSERT INTO repeats VALUES($1,$1,1)"} {
		_, err = e.Describe(op, statement(t, q))
		state(t, err, "0A000")
	}
	run(t, e, "INSERT INTO typed VALUES(1,NULL,NULL,NULL)")
	old := run(t, e, "SELECT * FROM typed")
	values := make([]sqltype.Value, 4)
	first := independentFinalRows(old.Rows)
	if present, nextErr := first.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	first.Close()
	for i, v := range values {
		if v.Type() != desc.Results[i].Usage.Type || v.IsNull() != (i != 0) {
			t.Fatal(v)
		}
	}
	ref := e.domain.Ref(view.Epoch(), ObjectAddress{ClassID: TypeClassID, ObjectID: 23})
	other := New()
	if err = other.ValidateObjectRef(other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign type ref")
	}
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.ValidateObjectRef(view, ref); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(23)
	if current == desc.Results[0].Usage.Type {
		t.Fatal("reused type generation")
	}
	later := independentFinalRows(old.Rows)
	if present, nextErr := later.Next(context.Background(), values); !present || nextErr != nil {
		t.Fatal(present, nextErr)
	}
	later.Close()
	if values[0].Type() != desc.Results[0].Usage.Type {
		t.Fatal("retained type changed")
	}
}

func TestTypeCoreCatalogRejectsNoncurrentColumnTypes(t *testing.T) {
	e := New()
	stale := testUsage(e, sqltype.Int4)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []sqltype.Usage{{}, testUsage(e, sqltype.Unknown), testUsage(New(), sqltype.Int4), stale} {
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		descriptor := &relationDescriptor{
			address:   ObjectAddress{ClassID: RelationClassID, ObjectID: 20000},
			namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID},
			name:      "invalid", revision: 1,
			columns: []ColumnDescriptor{{Name: "id", Usage: usage}},
			index:   map[string]int{"id": 0}, attributes: []int32{1},
			attributeHighWater: 1,
		}
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("invalid column type accepted", usage)
		}
		if e.CatalogRead().state != base.state {
			t.Fatal("invalid type published")
		}
	}
}

func TestFloatCatalogDescriptionsAndOwnership(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE f(a REAL NOT NULL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53),j "float4",k "float8")`)
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, statement(t, "SELECT * FROM f"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{700, 700, 701, 701, 701, 700, 700, 701, 701, 700, 701} {
		c := desc.Results[i]
		if c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Usage.Collation != (sqltype.CollationRef{}) || c.Nullable != (i != 0) || !c.NullabilityKnown || c.OriginTable == 0 || c.OriginTable != desc.Results[0].OriginTable || c.OriginAttribute != int16(i+1) {
			t.Fatal(c)
		}
	}
	bound, err := e.Describe(op, statement(t, "INSERT INTO f(a,c) VALUES($1,$2)"))
	if err != nil || bound.Parameters.Types[0].Type.ID() != 700 || bound.Parameters.Types[1].Type.ID() != 701 {
		t.Fatal(bound, err)
	}
}

func TestFloatFailedRestorePreservesEpochAndCatalog(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE f(r REAL,d DOUBLE PRECISION)")
	run(t, e, "INSERT INTO f VALUES('7.038531e-26','-0')")
	before := e.CatalogRead()
	var original bytes.Buffer
	if err := e.Dump(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')", "CREATE TABLE replacement(d DOUBLE PRECISION); INSERT INTO replacement VALUES('1'),('1e999')"} {
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore")
		}
		if current := e.CatalogRead(); current.state != before.state || current.Epoch() != before.Epoch() {
			t.Fatal("failed float restore changed catalog generation")
		}
		var dump bytes.Buffer
		if err := e.Dump(context.Background(), &dump); err != nil || dump.String() != original.String() {
			t.Fatal(dump.String(), err)
		}
	}
}
