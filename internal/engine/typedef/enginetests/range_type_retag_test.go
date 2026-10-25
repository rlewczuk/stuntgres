package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeStructuralRetag(t *testing.T) {
	_, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	_, nextR, nextM := rangeTypeUnitFamily(t, sqltype.Int4, true)
	wide := "{[-2147483648,-2147483647],[2147483646,2147483647]}"
	source := rangeTypeUnitInput(t, m, wide)
	// The new generation has genuine int4mi support. It overflows comparing the
	// separated members; structural Retag must not execute it, even before its
	// exact-generation fast return. Output, outside any binder lock, still does.
	moved, err := source.Retag(nextM)
	if err != nil {
		t.Fatal("generation transfer evaluated comparator", err)
	}
	var state *sqlstate.Error
	if _, err := nextM.Output(context.Background(), moved); !errors.As(err, &state) || state.Code != "22003" {
		t.Fatal("actual provider failure witness", err)
	}
	var got sqltype.Value
	if allocs := testing.AllocsPerRun(100, func() { got, err = moved.Retag(nextM) }); allocs != 0 || err != nil || !sqltype.SameDatum(got, moved) {
		t.Fatal("same-generation Retag must retain payload without providers or allocation", allocs, err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		got, err = moved.MapRange(nextM, sqltype.RangeMapGeneration, func(v sqltype.Value, d *sqltype.Descriptor) (sqltype.Value, error) { return v.Retag(d) })
	}); allocs != 0 || err != nil || !sqltype.SameDatum(got, moved) {
		t.Fatal("unchanged MapRange", allocs, err)
	}
	definition, _ := nextR.RangeDefinition()
	if err := moved.VisitRangeEndpoints(func(v sqltype.Value) error {
		if v.Type() != definition.Subtype.Type {
			t.Fatal("foreign endpoint generation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rangeTypeUnitOutput(t, source, wide)
	if _, err := source.MapRange(nextM, sqltype.RangeMapGeneration, func(_ sqltype.Value, d *sqltype.Descriptor) (sqltype.Value, error) { return sqltype.Integer(d, 0) }); err == nil {
		t.Fatal("generation-only mapping changed comparison semantics")
	}

	// Build actual nested carriers over each genuine family generation.
	carriers := func(r, m *sqltype.Descriptor) ([]sqltype.Value, []*sqltype.Descriptor) {
		rv := rangeTypeUnitInput(t, r, "[1,3]")
		mv := rangeTypeUnitInput(t, m, "{[1,3],[8,9]}")
		domain, e := sqltype.Domain(940001, "retag_many", "public.retag_many", m.Usage())
		if e != nil {
			t.Fatal(e)
		}
		wrapped, e := domain.Wrap(mv)
		if e != nil {
			t.Fatal(e)
		}
		array, e := sqltype.Array(940002, "_retag_many", domain)
		if e != nil {
			t.Fatal(e)
		}
		elements, e := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: -2, Length: 2}}, []sqltype.Value{wrapped, sqltype.Null(domain)})
		if e != nil {
			t.Fatal(e)
		}
		composite, e := sqltype.Composite(940003, "retag_record", "public.retag_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "r", Usage: r.Usage()}, {Attribute: 2, Name: "m", Usage: m.Usage()}, {Attribute: 3, Name: "a", Usage: array.Usage()}})
		if e != nil {
			t.Fatal(e)
		}
		record, e := sqltype.CompositeValue(composite, []sqltype.Value{rv, mv, elements})
		if e != nil {
			t.Fatal(e)
		}
		outer, e := sqltype.Array(940004, "_retag_record", composite)
		if e != nil {
			t.Fatal(e)
		}
		nested, e := sqltype.ArrayValue(outer, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{record})
		if e != nil {
			t.Fatal(e)
		}
		return []sqltype.Value{rv, mv, wrapped, elements, record, nested, sqltype.Null(m), rangeTypeUnitInput(t, m, "{}")}, []*sqltype.Descriptor{r, m, domain, array, composite, outer, m, m}
	}
	values, _ := carriers(r, m)
	_, targets := carriers(nextR, nextM)
	var inspect func(sqltype.Value)
	inspect = func(v sqltype.Value) {
		if v.IsNull() {
			return
		}
		base := v.Type().BaseType()
		if base.RangeType() != nil {
			if base.RangeType() != nextR {
				t.Fatal("stale family capture")
			}
			if err := v.VisitRangeEndpoints(func(endpoint sqltype.Value) error {
				if endpoint.Type() != definition.Subtype.Type {
					t.Fatal("stale nested endpoint")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if base.IsArray() {
			for i := range v.ArrayLen() {
				inspect(v.ArrayElement(i))
			}
		}
		if base.IsComposite() {
			for i := range v.CompositeLen() {
				inspect(v.CompositeField(i))
			}
		}
	}
	for i, value := range values {
		var unchanged sqltype.Value
		if allocs := testing.AllocsPerRun(100, func() { unchanged, err = value.Retag(value.Type()) }); allocs != 0 || err != nil || !sqltype.SameDatum(unchanged, value) {
			t.Fatal("unchanged nested carrier allocation", i, allocs, err)
		}
		mapped, e := value.Retag(targets[i])
		if e != nil || mapped.Type() != targets[i] {
			t.Fatal("nested generation transfer", i, e)
		}
		if value.IsNull() {
			if !mapped.IsNull() {
				t.Fatal("typed NULL changed")
			}
			continue
		}
		originalText, e := value.Type().Output(context.Background(), value)
		if e != nil {
			t.Fatal(e)
		}
		inspect(mapped)
		text, e := targets[i].Output(context.Background(), mapped)
		if e != nil || text != originalText {
			t.Fatal("nested value changed", i, text, originalText, e)
		}
		rangeTypeUnitOutput(t, value, originalText)
	}
	if _, err := source.Retag(r); err == nil {
		t.Fatal("wrong family admitted")
	}
}
