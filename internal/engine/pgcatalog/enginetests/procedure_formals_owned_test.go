package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestRoutineResultFormalOwnedCatalog(t *testing.T) {
	e := engine.New()
	for _, ddl := range []string{
		"CREATE EXTENSION pgrowlocks", "CREATE EXTENSION amcheck",
		`CREATE AGGREGATE formal_unit_rank(VARIADIC "any" ORDER BY VARIADIC "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,
		`CREATE AGGREGATE formal_unit_fixed(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')`,
		"CREATE TYPE formal_unit_range AS RANGE(SUBTYPE=integer,MULTIRANGE_TYPE_NAME=formal_unit_many)",
	} {
		enginetest.Run(t, e, ddl)
	}
	view := e.CatalogRead()
	want := map[ids.OID]string{}
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		switch d.Name() {
		case "pgrowlocks":
			want[d.Address().ObjectID] = "{25,27,28,16,1011,1009,1007}|{i,o,o,o,o,o,o}"
		case "verify_heapam":
			want[d.Address().ObjectID] = "{2205,16,16,25,20,20,20,23,23,25}|{i,i,i,i,i,i,o,o,o,o}"
		case "formal_unit_rank":
			want[d.Address().ObjectID] = "{2276}|{v}"
		case "formal_unit_fixed":
			want[d.Address().ObjectID] = ""
		}
		if d.RangeConstructor != nil {
			want[d.Address().ObjectID] = ""
			if d.RangeConstructor.Form == catalog.MultirangeConstructorMany {
				owner, ok := view.LookupType(d.RangeConstructor.Type.RangeType().ID())
				if !ok {
					t.Fatal("missing range owner")
				}
				rangeType := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(owner.ID())})
				want[d.Address().ObjectID] = fmt.Sprintf("{%d}|{v}", rangeType.Array)
				if d.RangeConstructor.Variadic() != ids.OID(owner.ID()) || d.Signature != catalog.ProcedureSignature(fmt.Sprint(rangeType.Array)) {
					t.Fatal("constructor confused range element and declared array")
				}
			}
		}
	}
	if len(want) != 9 {
		t.Fatal("missing actual owned declarations", len(want))
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ProcedureClassID})
	produce, ok := pgcatalog.ProducerFor(ids.ProcedureClassID)
	if !ok {
		t.Fatal("missing procedure producer")
	}
	in := pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: e.Catalog.Domain.Ref(view.Epoch(), source.Address()), Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[ids.OID]bool{}
	for _, row := range captured.Anonymous {
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		expected, ok := want[ids.OID(id)]
		if !ok {
			continue
		}
		seen[ids.OID(id)] = true
		a, m := row[17], row[18]
		if a.Type() != source.Columns[17].Usage.Type || m.Type() != source.Columns[18].Usage.Type {
			t.Fatal("foreign formal array descriptor")
		}
		if expected == "" {
			if !a.IsNull() || !m.IsNull() {
				t.Error("all-IN must retain typed NULL", id)
			}
			continue
		}
		if a.IsNull() || m.IsNull() {
			t.Error("missing actual non-IN declaration", id)
			continue
		}
		at, err := a.Type().Output(in.Context, a)
		if err != nil {
			t.Fatal(err)
		}
		mt, err := m.Type().Output(in.Context, m)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(expected, "|")
		if at != parts[0] || mt != parts[1] || a.ArrayDimension(0).Lower != 1 || m.ArrayDimension(0).Lower != 1 {
			t.Error("owned formal arrays", id, at, mt, expected)
		}
	}
	if len(seen) != len(want) {
		t.Fatal("lost represented owner", len(seen), len(want))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in.Context = ctx
	if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled formal source capture", got, err)
	}
}
