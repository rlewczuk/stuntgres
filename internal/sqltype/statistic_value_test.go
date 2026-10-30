package sqltype

import (
	"context"
	"strings"
	"testing"
)

func statisticValueFields(t *testing.T, d *Descriptor) []Value {
	t.Helper()
	fields := make([]Value, d.CompositeLen())
	for i := range fields {
		member := d.CompositeMember(i).Usage.Type
		switch member.ID() {
		case Oid:
			fields[i], _ = OIDValue(member, 0)
		case Int2, Int4:
			fields[i], _ = Integer(member, 0)
		case Float4:
			fields[i], _ = Float(member, 0)
		case Bool:
			fields[i] = Boolean(member, false)
		default:
			fields[i] = ResultNull(member.Usage())
		}
	}
	return fields
}

func TestStatisticExpressionValueConstructionAndAccess(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	rowType, arrayType := types[10029], types[10028]
	fields := statisticValueFields(t, rowType)
	elements := []Value{}
	for _, n := range []int64{2, 3} {
		v, _ := Integer(types[Int4], n)
		elements = append(elements, v)
	}
	wrapped, err := StatisticArrayValue(types[2277], types[Int4].Usage(), []ArrayDimension{{1,2}}, elements)
	if err != nil {
		t.Fatal(err)
	}
	fields[26] = wrapped
	fields[6], _ = Integer(types[Int2], 2)
	fields[11], _ = OIDValue(types[Oid], 97)
	fields[3], _ = Float(types[Float4], 0.2)
	fields[5], _ = Float(types[Float4], 2)
	row, err := StatisticRowValue(rowType, fields)
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{row}
	array, err := StatisticRowsValue(arrayType, rows)
	if err != nil {
		t.Fatal(err)
	}
	fields[26] = ResultNull(types[2277].Usage())
	rows[0] = ResultNull(rowType.Usage())
	elements[0], _ = Integer(types[Int4], 99)
	fetched, err := ArrayFetch(ctx, array, []ArraySubscriptValue{{Upper: 1, HasUpper: true}})
	if err != nil {
		t.Fatal(err)
	}
	slot := fetched.CompositeField(26)
	usage, ok := slot.StatisticArrayUsage()
	actual := slot
	if !ok || usage != types[Int4].Usage() {
		t.Fatal("lost typed element authority", usage)
	}
	if n, _ := actual.ArrayElement(0).Integer(); n != 2 {
		t.Fatal("borrowed construction buffer", n)
	}
	cast, err := PlanCoercion(slot.Type().Usage(), types[Text].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	text, err := cast.Apply(ctx, slot, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := text.Text(); got != "{2,3}" {
		t.Fatal(got)
	}
	output, err := arrayType.Output(ctx, array)
	if err != nil || !strings.HasPrefix(output, `{"(`) || !strings.HasSuffix(output, `)"}`) || !strings.Contains(output, `{2,3}`) {
		t.Fatal(output, err)
	}
	for _, index := range []int32{0, 2} {
		missing, err := ArrayFetch(ctx, array, []ArraySubscriptValue{{Upper: index, HasUpper: true}})
		if err != nil || !missing.IsNull() || !missing.ValidResult(rowType.Usage()) {
			t.Fatal("typed missing row", err)
		}
	}
	if !array.ValidResult(arrayType.Usage()) || !row.ValidResult(rowType.Usage()) || !slot.ValidResult(types[2277].Usage()) {
		t.Fatal("authentic result rejected")
	}
	if _, err := CompositeValue(rowType, statisticValueFields(t, rowType)); err == nil {
		t.Fatal("generic row constructor gained authority")
	}
	if _, err := ArrayValue(arrayType, []ArrayDimension{{1, 1}}, []Value{row}); err == nil {
		t.Fatal("generic array constructor gained authority")
	}
}

func TestStatisticExpressionValueGenerationAndImport(t *testing.T) {
	from, to := testTypes(), testTypes()
	ctx := context.Background()
	concrete, err := from[1009].Input(ctx, `{"a,b","quoted\"value"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := StatisticArrayValue(from[2277], from[Text].Usage(), concrete.ArrayDimensions(), concrete.array.elements)
	if err != nil {
		t.Fatal(err)
	}
	fields := statisticValueFields(t, from[10029])
	fields[26] = slot
	fields[6], _ = Integer(from[Int2], 2)
	fields[11], _ = OIDValue(from[Oid], 664)
	row, err := StatisticRowValue(from[10029], fields)
	if err != nil {
		t.Fatal(err)
	}
	array, err := StatisticRowsValue(from[10028], []Value{row})
	if err != nil {
		t.Fatal(err)
	}
	retagged, err := array.Retag(to[10028])
	if err != nil {
		t.Fatal(err)
	}
	if retagged.Type() != to[10028] || retagged.ArrayElement(0).Type() != to[10029] || retagged.ArrayElement(0).CompositeField(26).Type() != to[2277] {
		t.Fatal("foreign outer generation")
	}
	same, err := array.SamePayload(ctx, retagged)
	if err != nil || !same {
		t.Fatal("retag changed payload", err)
	}
	imported, err := slot.MapStatisticArray(to[2277], func(array Value, usage Usage) (Value, Usage, error) {
		next, err := array.Retag(to[array.Type().ID()])
		usage.Type = to[usage.Type.ID()]
		return next, usage, err
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := imported.StatisticArrayUsage()
	inner := imported
	if !ok || usage.Type != to[Text] || inner.ArrayElement(0).Type() != to[Text] {
		t.Fatal("import did not remint typed payload")
	}
	if same, err := slot.SamePayload(ctx, imported); err != nil || !same {
		t.Fatal("import changed datum", err)
	}
	output, err := from[10028].Output(ctx, array)
	if err != nil {
		t.Fatal(err)
	}
	output2, err := to[10028].Output(ctx, retagged)
	if err != nil || output2 != output {
		t.Fatal("generation output changed", output2, err)
	}
	copy := *to[2277]
	if _, err := slot.Retag(&copy); err == nil {
		t.Fatal("copied pseudo minted authority")
	}
	fields[4], _ = Integer(to[Int4], 4)
	if _, err := StatisticRowValue(from[10029], fields); err == nil {
		t.Fatal("foreign row member accepted")
	}
	if _, err := StatisticRowsValue(to[10028], []Value{row}); err == nil {
		t.Fatal("foreign row array accepted")
	}
	if _, err := StatisticArrayValue(to[2277], to[Text].Usage(), concrete.ArrayDimensions(), concrete.array.elements); err == nil {
		t.Fatal("foreign concrete element usage accepted")
	}
	bad := Value{typ: from[2277]}
	if bad.ValidResult(from[2277].Usage()) {
		t.Fatal("unconstructed pseudo accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := from[10028].Output(canceled, array); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestStatisticExpressionValueSignedDimensions(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, witness := range []struct {
		attributes []int16
		output     string
	}{
		{[]int16{2, -1}, `{"2, -1": 3}`},
		{[]int16{-1, -2}, `{"-1, -2": 3}`},
		{[]int16{2, -1, -2}, `{"2, -1, -2": 3}`},
	} {
		value, err := StatisticsValue(types[PgNDistinct], []StatisticsItem{{Attributes: witness.attributes, Value: 3}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := types[PgNDistinct].Output(ctx, value)
		if err != nil || text != witness.output {
			t.Fatal(text, err)
		}
	}
	for _, attributes := range [][]int16{{0, 1}, {-9, 1}, {-1, -1}, {-1, 2}, {-2, -1}, {2, 1}} {
		if _, err := StatisticsValue(types[PgNDistinct], []StatisticsItem{{Attributes: attributes, Value: 3}}); err == nil {
			t.Fatal("invalid dimensions", attributes)
		}
	}
}

func TestStatisticExpressionValueSlotValidation(t *testing.T) {
	types := testTypes()
	rowType := types[10029]
	fields := statisticValueFields(t, rowType)
	empty, err := StatisticRowValue(rowType, fields)
	if err != nil || !empty.ValidResult(rowType.Usage()) {
		t.Fatal("empty statistic slots", err)
	}
	values, err := types[1007].Input(t.Context(), "{2,3}", -1)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := StatisticArrayValue(types[2277], types[Int4].Usage(), values.ArrayDimensions(), values.array.elements)
	if err != nil {
		t.Fatal(err)
	}
	fields[26] = slot
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("unused slot carried values")
	}
	fields[6], _ = Integer(types[Int2], 1)
	fields[11], _ = OIDValue(types[Oid], 96)
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("MCV missing frequencies")
	}
	numbers, err := types[1021].Input(t.Context(), "{0.7,0.1}", -1)
	if err != nil {
		t.Fatal(err)
	}
	fields[21] = numbers
	mcv, err := StatisticRowValue(rowType, fields)
	if err != nil || !mcv.ValidResult(rowType.Usage()) {
		t.Fatal("genuine MCV", err)
	}
	fields[21], _ = types[1021].Input(t.Context(), "{0.7}", -1)
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("frequency/value arity mismatch")
	}
	fields[21], _ = types[1021].Input(t.Context(), "{0.7,0.7}", -1)
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("impossible frequencies")
	}
	fields[21] = numbers
	fields[6], _ = Integer(types[Int2], 2)
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("histogram carried MCV frequencies")
	}
	fields[21] = Null(types[1021])
	if _, err := StatisticRowValue(rowType, fields); err != nil {
		t.Fatal("genuine histogram", err)
	}
	fields[3], _ = Float(types[Float4], 1.1)
	if _, err := StatisticRowValue(rowType, fields); err == nil {
		t.Fatal("impossible null fraction")
	}
}

func TestAttributeStatisticsArrayElementContainer(t *testing.T) {
	from,to:=testTypes(),testTypes()
	ctx:=context.Background()
	usage:=from[1007].Usage()
	value,err:=ParseStatisticArray(ctx,from[2277],usage,`[0:1]={"{1,2}","{3}"}`,InputEnvironment{})
	if err!=nil {t.Fatal(err)}
	text,err:=from[2277].Output(ctx,value)
	if err!=nil||text!=`[0:1]={"{1,2}","{3}"}` {t.Fatal(text,err)}
	if u,ok:=value.StatisticArrayUsage();!ok||u!=usage||value.ArrayRank()!=1||value.ArrayDimension(0).Lower!=0||value.ArrayElement(0).Type()!=from[1007] {t.Fatal("lost nested element authority")}
	first,err:=ArrayFetch(ctx,value,[]ArraySubscriptValue{{Upper:0,HasUpper:true}})
	if err!=nil||first.Type()!=from[1007]||first.ArrayLen()!=2 {t.Fatal(first,err)}
	mapped,err:=value.MapStatisticArray(to[2277],func(element Value,u Usage)(Value,Usage,error){v,err:=element.Retag(to[u.Type.ID()]);u.Type=to[u.Type.ID()];return v,u,err})
	if err!=nil||mapped.ArrayElement(0).Type()!=to[1007]||mapped.ArrayElement(0).ArrayElement(0).Type()!=to[Int4] {t.Fatal("nested remint",err)}
	if _,err=StatisticArrayValue(to[2277],to[1007].Usage(),value.ArrayDimensions(),value.array.elements);err==nil {t.Fatal("foreign nested elements admitted")}
	if _,err=Array(48000,"illegal",from[1007]);err==nil {t.Fatal("catalog result expanded general array admission")}
}
