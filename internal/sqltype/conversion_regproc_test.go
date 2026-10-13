package sqltype

import (
	"context"
	"testing"
)

func TestConversionRegprocDerivedCarrier(t *testing.T) {
	ctx, types := context.Background(), testTypes()
	d := types[Regproc]
	if d == nil || !d.Concrete() || d.ID() != 24 || d.DatabaseName() != "REGPROC" || types[1008].Concrete() || !d.ContainsCatalogIdentifier() {
		t.Fatal("derived REGPROC inventory/capability", d)
	}
	for _, id := range []uint32{0, 4375, 2147483648, 4294967295} {
		value, err := RegprocValue(d, id)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := value.ProcedureOID()
		if err != nil || actual != id {
			t.Fatal("unsigned procedure identity", actual, err)
		}
		plan, err := PlanCoercion(d.Usage(), types[Oid].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		converted, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		oid, err := converted.OID()
		if err != nil || oid != id {
			t.Fatal("REGPROC -> OID", oid, err)
		}
		order, err := BindOrdering(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if comparison, err := order.Compare(ctx, value, value, InputEnvironment{}); err != nil || comparison != 0 {
			t.Fatal("identity ordering", comparison, err)
		}
		group, err := BindGrouping(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if equal, err := group.Equal(ctx, value, value); err != nil || !equal {
			t.Fatal("identity equality", equal, err)
		}
		if _, err := d.OutputWithEnvironment(ctx, value, OutputEnvironment{}); err == nil {
			t.Fatal("REGPROC output disguised as numeric OID without captured presentation")
		}
	}
	for _, input := range []string{"4375", "utf8_to_iso8859_1", "pg_catalog.utf8_to_iso8859_1"} {
		if _, err := d.Input(ctx, input, -1); err == nil {
			t.Fatal("invented generic REGPROC input", input)
		}
	}
	for _, source := range []*Descriptor{types[Text], types[Oid], types[Int4], types[Regtype]} {
		if _, err := PlanCoercion(source.Usage(), d.Usage(), Explicit); err == nil {
			t.Fatal("invented REGPROC input cast", source)
		}
	}
	for _, target := range []*Descriptor{types[Int4], types[Regtype], types[Regclass], types[VarChar]} {
		if _, err := PlanCoercion(d.Usage(), target.Usage(), Explicit); err == nil {
			t.Fatal("unbounded REGPROC cast", target)
		}
	}
	text, err := PlanCoercion(d.Usage(), types[Text].Usage(), Explicit)
	if err != nil || !text.RequiresCatalogOutput() {
		t.Fatal("REGPROC text cast lacks output capture demand", text, err)
	}
	null, err := text.Apply(ctx, Null(d), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !null.IsNull() || null.Type() != types[Text] {
		t.Fatal("typed NULL text inspection", null, err)
	}
	if _, err := RegprocValue(types[Oid], 4375); err == nil {
		t.Fatal("OID mislabeled REGPROC")
	}
}
